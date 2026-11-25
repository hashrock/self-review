// Package agent talks to the coding agent the user already uses (Claude Code,
// Gemini CLI, Codex, ...) through the Agent Client Protocol (ACP): the agent
// runs as a child process speaking JSON-RPC over stdio.
//
// The agent is kept read-only: sessions are switched to their plan or
// read-only mode, the client offers no file writing or terminals, and every
// permission request for anything but reading or searching is rejected.
package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/coder/acp-go-sdk"
)

// Agents self-review knows how to start. Versions are pinned: the adapters
// change almost daily.
var Agents = map[string]Spec{
	"claude": {
		Title:   "Claude Code",
		Command: []string{"npx", "-y", "@agentclientprotocol/claude-agent-acp@0.112.0"},
	},
	"gemini": {
		Title:   "Gemini CLI",
		Command: []string{"gemini", "--acp", "--approval-mode", "plan"},
	},
	"codex": {
		Title:   "Codex",
		Command: []string{"npx", "-y", "@agentclientprotocol/codex-acp@2.12.0"},
		Env:     []string{"INITIAL_AGENT_MODE=read-only"},
	},
}

// Names lists the known agents in a stable order.
var Names = []string{"claude", "gemini", "codex"}

// Spec is how to start an agent.
type Spec struct {
	Title   string
	Command []string
	Env     []string
}

// Event is something the agent did while answering.
type Event struct {
	Text string // a piece of the answer
	Tool string // a tool it started, by title (reading a file, searching...)
}

// Client is a running agent.
type Client struct {
	Title string

	cmd    *exec.Cmd
	conn   *acp.ClientSideConnection
	cwd    string
	stderr *bytes.Buffer

	mu       sync.Mutex
	handlers map[acp.SessionId]func(Event)
}

// Start runs the agent in cwd and initializes the connection. ctx bounds
// the start only; Close ends the agent.
func Start(ctx context.Context, spec Spec, cwd string) (*Client, error) {
	if len(spec.Command) == 0 {
		return nil, errors.New("no agent command")
	}
	cmd := exec.Command(spec.Command[0], spec.Command[1:]...)
	cmd.Dir = cwd
	cmd.Env = append(withLoginPath(os.Environ()), spec.Env...)
	cmd.SysProcAttr = ownGroup()
	stderr := &bytes.Buffer{}
	cmd.Stderr = &limitedWriter{buf: stderr, max: 64 << 10}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("cannot start %s (%s): %w", spec.Title, strings.Join(spec.Command, " "), err)
	}
	c := &Client{Title: spec.Title, cmd: cmd, cwd: cwd, stderr: stderr, handlers: map[acp.SessionId]func(Event){}}
	c.conn = acp.NewClientSideConnection(c, stdin, stdout)

	// No file writing, no terminals: the agent only reads, on its own.
	if _, err := c.conn.Initialize(ctx, acp.InitializeRequest{
		ProtocolVersion:    acp.ProtocolVersionNumber,
		ClientCapabilities: acp.ClientCapabilities{},
		ClientInfo:         &acp.Implementation{Name: "self-review"},
	}); err != nil {
		c.Close()
		return nil, c.explain("initialize", err)
	}
	return c, nil
}

// explain adds what the agent wrote to stderr to an error.
func (c *Client) explain(what string, err error) error {
	msg := strings.TrimSpace(c.stderr.String())
	if len(msg) > 600 {
		msg = "…" + msg[len(msg)-600:]
	}
	if msg != "" {
		return fmt.Errorf("%s: %w\n%s", what, err, msg)
	}
	return fmt.Errorf("%s: %w", what, err)
}

// Close ends the agent and everything it started.
func (c *Client) Close() {
	if c.cmd != nil && c.cmd.Process != nil {
		killGroup(c.cmd.Process)
		_ = c.cmd.Wait()
	}
}

// Session is one conversation with the agent.
type Session struct {
	c  *Client
	id acp.SessionId
}

// readOnlyModes are session modes that keep an agent from changing files,
// most preferred first.
var readOnlyModes = []string{"plan", "read-only", "readonly", "ask"}

// NewSession starts a conversation in the client's directory, in a
// read-only mode when the agent offers one.
func (c *Client) NewSession(ctx context.Context) (*Session, error) {
	resp, err := c.conn.NewSession(ctx, acp.NewSessionRequest{Cwd: c.cwd, McpServers: []acp.McpServer{}})
	if err != nil {
		return nil, c.explain("new session", err)
	}
	if resp.Modes != nil {
		for _, want := range readOnlyModes {
			for _, m := range resp.Modes.AvailableModes {
				if string(m.Id) == want {
					if string(resp.Modes.CurrentModeId) != want {
						if _, err := c.conn.SetSessionMode(ctx, acp.SetSessionModeRequest{SessionId: resp.SessionId, ModeId: m.Id}); err != nil {
							return nil, c.explain("set read-only mode", err)
						}
					}
					return &Session{c: c, id: resp.SessionId}, nil
				}
			}
		}
	}
	return &Session{c: c, id: resp.SessionId}, nil
}

// Prompt sends text and calls on for each piece of the answer until the
// turn ends.
func (s *Session) Prompt(ctx context.Context, text string, on func(Event)) error {
	s.c.mu.Lock()
	s.c.handlers[s.id] = on
	s.c.mu.Unlock()
	defer func() {
		s.c.mu.Lock()
		delete(s.c.handlers, s.id)
		s.c.mu.Unlock()
	}()
	resp, err := s.c.conn.Prompt(ctx, acp.PromptRequest{SessionId: s.id, Prompt: []acp.ContentBlock{acp.TextBlock(text)}})
	if err != nil {
		if ctx.Err() != nil {
			_ = s.c.conn.Cancel(context.Background(), acp.CancelNotification{SessionId: s.id})
			return ctx.Err()
		}
		return s.c.explain("prompt", err)
	}
	if resp.StopReason == acp.StopReasonRefusal {
		return errors.New("the agent declined to answer")
	}
	return nil
}

// ---------- acp.Client: what the agent may ask of us ----------

var _ acp.Client = (*Client)(nil)

// allowedKinds are the tool calls a read-only review may make.
var allowedKinds = map[acp.ToolKind]bool{acp.ToolKindRead: true, acp.ToolKindSearch: true, acp.ToolKindThink: true}

// RequestPermission allows reading and searching once, and rejects the rest.
func (c *Client) RequestPermission(ctx context.Context, p acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	kind, _ := p.ToolCall.Kind.Value()
	return acp.RequestPermissionResponse{Outcome: Decide(kind, p.Options)}, nil
}

// Decide picks the answer to a permission request: allow once for reading
// and searching, reject (once) for anything else, or cancel when the agent
// offers no fitting option, which agents must not take as approval.
func Decide(kind acp.ToolKind, options []acp.PermissionOption) acp.RequestPermissionOutcome {
	pick := func(kinds ...acp.PermissionOptionKind) *acp.RequestPermissionOutcome {
		for _, k := range kinds {
			for _, o := range options {
				if o.Kind == k {
					return &acp.RequestPermissionOutcome{Selected: &acp.RequestPermissionOutcomeSelected{OptionId: o.OptionId}}
				}
			}
		}
		return nil
	}
	var out *acp.RequestPermissionOutcome
	if allowedKinds[kind] {
		out = pick(acp.PermissionOptionKindAllowOnce)
	} else {
		out = pick(acp.PermissionOptionKindRejectOnce, acp.PermissionOptionKindRejectAlways)
	}
	if out == nil {
		return acp.RequestPermissionOutcome{Cancelled: &acp.RequestPermissionOutcomeCancelled{}}
	}
	return *out
}

// SessionUpdate passes the answer's text and tool calls to the session's handler.
func (c *Client) SessionUpdate(ctx context.Context, n acp.SessionNotification) error {
	c.mu.Lock()
	on := c.handlers[n.SessionId]
	c.mu.Unlock()
	if on == nil {
		return nil
	}
	u := n.Update
	switch {
	case u.AgentMessageChunk != nil && u.AgentMessageChunk.Content.Text != nil:
		on(Event{Text: u.AgentMessageChunk.Content.Text.Text})
	case u.ToolCall != nil:
		on(Event{Tool: u.ToolCall.Title})
	}
	return nil
}

func (c *Client) ReadTextFile(context.Context, acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	return acp.ReadTextFileResponse{}, acp.NewMethodNotFound(acp.ClientMethodFsReadTextFile)
}

func (c *Client) WriteTextFile(context.Context, acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	return acp.WriteTextFileResponse{}, acp.NewMethodNotFound(acp.ClientMethodFsWriteTextFile)
}

func (c *Client) CreateTerminal(context.Context, acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return acp.CreateTerminalResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalCreate)
}

func (c *Client) KillTerminal(context.Context, acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	return acp.KillTerminalResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalKill)
}

func (c *Client) TerminalOutput(context.Context, acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalOutput)
}

func (c *Client) ReleaseTerminal(context.Context, acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalRelease)
}

func (c *Client) WaitForTerminalExit(context.Context, acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalWaitForExit)
}

func (c *Client) CreateElicitation(context.Context, acp.CreateElicitationRequest) (acp.CreateElicitationResponse, error) {
	return acp.CreateElicitationResponse{}, acp.NewMethodNotFound(acp.ClientMethodElicitationCreate)
}

func (c *Client) CompleteElicitation(context.Context, acp.CompleteElicitationNotification) error {
	return nil
}

// ---------- process helpers ----------

type limitedWriter struct {
	mu  sync.Mutex
	buf *bytes.Buffer
	max int
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf.Write(p)
	if over := w.buf.Len() - w.max; over > 0 {
		w.buf.Next(over)
	}
	return len(p), nil
}

var (
	loginPathOnce sync.Once
	loginPath     string
)

// withLoginPath gives env the PATH of the user's login shell, so agents
// installed with npm, mise, Homebrew... are found when the app was opened
// from Finder, whose PATH is minimal.
func withLoginPath(env []string) []string {
	loginPathOnce.Do(func() { loginPath = shellPath() })
	if loginPath == "" {
		return env
	}
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if !strings.HasPrefix(kv, "PATH=") {
			out = append(out, kv)
		}
	}
	return append(out, "PATH="+loginPath)
}
