package app

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/hashrock/self-review/internal/agent"
	"github.com/hashrock/self-review/internal/repo"
)

// asker starts conversations with the reader's coding agent: a real ACP
// agent, or a fake in tests.
type asker interface {
	NewSession(ctx context.Context) (conversation, error)
	Close()
}

type conversation interface {
	Prompt(ctx context.Context, text string, on func(agent.Event)) error
}

// acpAsker runs the agent of a Spec, started at the first question.
type acpAsker struct{ c *agent.Client }

func (a acpAsker) NewSession(ctx context.Context) (conversation, error) { return a.c.NewSession(ctx) }
func (a acpAsker) Close()                                               { a.c.Close() }

// agentHolder starts the agent once, when the first question needs it, and
// ends it with the window.
type agentHolder struct {
	mu    sync.Mutex
	start func(ctx context.Context) (asker, error)
	a     asker
}

func (h *agentHolder) get(ctx context.Context) (asker, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.a == nil {
		a, err := h.start(ctx)
		if err != nil {
			return nil, err
		}
		h.a = a
	}
	return h.a, nil
}

func (h *agentHolder) close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.a != nil {
		h.a.Close()
		h.a = nil
	}
}

type threadKind int

const (
	threadChange  threadKind = iota // the change as a whole
	threadSection                   // a section of the guide
	threadLine                      // lines of a file
)

// thread is one conversation with the agent, about the change, a section of
// the guide, or some lines. Only the main thread touches it.
type thread struct {
	id      int
	view    string
	kind    threadKind
	label   string
	section string
	path    string
	side    editor.DiffSide
	start   int // file lines, from 1
	end     int
	msgs    []message
	draft   string
	busy    bool
	conv    conversation
	cancel  context.CancelFunc
	focus   bool // bring into view and focus its input on the next frame
}

type message struct {
	user  bool
	text  string
	tools []string
	err   string
}

// setAgent chooses the agent questions go to.
func (a *App) setAgent(spec agent.Spec) {
	if a.agents != nil {
		a.agents.close()
	}
	a.agentTitle = spec.Title
	ws := a.ws
	a.agents = &agentHolder{start: func(ctx context.Context) (asker, error) {
		c, err := agent.Start(ctx, spec, ws.Repo.Root)
		if err != nil {
			return nil, err
		}
		return acpAsker{c}, nil
	}}
}

func (a *App) viewThreads() []*thread {
	var out []*thread
	for _, th := range a.threads {
		if th.view == a.view {
			out = append(out, th)
		}
	}
	return out
}

// newThread opens a conversation and shows it, or the one already about
// the same thing.
func (a *App) newThread(th thread) *thread {
	for _, old := range a.viewThreads() {
		if old.kind == th.kind && old.kind != threadChange && old.section == th.section &&
			old.path == th.path && old.side == th.side && old.start == th.start && old.end == th.end {
			a.focusThread(old)
			return old
		}
	}
	a.threadSeq++
	th.id, th.view = a.threadSeq, a.view
	t := &th
	a.threads = append(a.threads, t)
	a.focusThread(t)
	return t
}

func (a *App) focusThread(th *thread) {
	a.questionsOpen = true
	th.focus = true
}

func (a *App) deleteThread(th *thread) {
	if th.cancel != nil {
		th.cancel()
	}
	for i, t := range a.threads {
		if t == th {
			a.threads = append(a.threads[:i], a.threads[i+1:]...)
			return
		}
	}
}

func (a *App) askLines(path string, side editor.DiffSide, start, end int) {
	label := fmt.Sprintf("%s:%d", path, start)
	if end > start {
		label += fmt.Sprintf("-%d", end)
	}
	a.newThread(thread{kind: threadLine, label: label, path: path, side: side, start: start, end: end})
}

func (a *App) askSection(title string) {
	a.newThread(thread{kind: threadSection, label: fmt.Sprintf(a.tx.SectionLabel, title), section: title})
}

func (a *App) askChange() {
	a.newThread(thread{kind: threadChange, label: a.tx.WholeChange})
}

// update runs fn on the main thread (at once without a window, in tests).
func (a *App) update(fn func()) {
	if a.win != nil {
		a.win.Update(fn)
	} else {
		fn()
	}
}

// send asks the thread's draft. The first question of a thread carries the
// context: what changed, where the guide is, and the lines or section asked
// about.
func (a *App) send(th *thread) {
	text := strings.TrimSpace(th.draft)
	if text == "" || th.busy || a.page == nil || a.agents == nil {
		return
	}
	th.draft = ""
	th.msgs = append(th.msgs, message{user: true, text: text}, message{})
	th.busy = true
	ctx, cancel := context.WithCancel(context.Background())
	th.cancel = cancel
	conv := th.conv
	l := a.page.loaded
	cmp, guidePath, hasGuide := l.Comparison, l.GuidePath, l.Guide != nil
	q := *th // what the context needs; fixed once the thread is made
	work := func() {
		defer cancel()
		var err error
		prompt := text
		if conv == nil {
			var ag asker
			if ag, err = a.agents.get(ctx); err == nil {
				conv, err = ag.NewSession(ctx)
			}
			if err == nil {
				c := conv
				a.update(func() { th.conv = c })
				prompt = a.threadContext(&q, cmp, guidePath, hasGuide) + "\n\n" + text
			}
		}
		if err == nil {
			err = conv.Prompt(ctx, prompt, func(e agent.Event) {
				a.update(func() {
					last := &th.msgs[len(th.msgs)-1]
					last.text += e.Text
					if e.Tool != "" {
						last.tools = append(last.tools, e.Tool)
					}
				})
			})
		}
		a.update(func() {
			th.busy, th.cancel = false, nil
			if err != nil && ctx.Err() == nil {
				th.msgs[len(th.msgs)-1].err = err.Error()
			}
		})
	}
	if a.win == nil {
		work()
		return
	}
	go work()
}

// languageNames name the readers' languages for the agent.
var languageNames = map[string]string{"en": "English", "ja": "Japanese"}

// threadContext tells the agent what the conversation is about.
func (a *App) threadContext(th *thread, cmp *repo.Comparison, guidePath string, hasGuide bool) string {
	var b strings.Builder
	lang := languageNames[a.lang]
	if lang == "" {
		lang = "English"
	}
	fmt.Fprintf(&b, "I am reviewing a code change in this repository with self-review. Answer my questions and review comments in %s. "+
		"Read whatever you need, but do not modify files or run commands that change anything.\n\n", lang)
	fmt.Fprintf(&b, "The change: %s. See it with `%s`.\n", cmp, gitCommand(cmp))
	if hasGuide {
		fmt.Fprintf(&b, "A guide that explains it is at %s.\n", guidePath)
	}
	switch th.kind {
	case threadChange:
		b.WriteString("\nMy question is about the change as a whole.")
	case threadSection:
		fmt.Fprintf(&b, "\nMy question is about the section %q of the guide.", th.section)
	case threadLine:
		fmt.Fprintf(&b, "\nMy question is about %s", th.label)
		if th.side == editor.DiffOld {
			b.WriteString(" (lines removed by the change, numbered as before it)")
		}
		b.WriteString(":\n\n```diff\n")
		b.WriteString(a.snippet(cmp, th))
		b.WriteString("```")
	}
	return b.String()
}

// gitCommand shows the change of a comparison.
func gitCommand(c *repo.Comparison) string {
	switch {
	case c.Mode == "commit":
		return "git show " + c.New
	case c.New == "":
		return "git diff " + c.Old
	}
	return "git diff " + c.Old + " " + c.New
}

// snippet is the asked lines with a little context, as a unified diff.
func (a *App) snippet(cmp *repo.Comparison, th *thread) string {
	fd, err := a.ws.Repo.Diff(cmp, th.path)
	if err != nil || fd.Binary {
		return "(not available)\n"
	}
	var b strings.Builder
	for _, g := range repo.Excerpt(fd.Lines, th.start, th.end, 2) {
		for _, i := range g {
			l := fd.Lines[i]
			sign := " "
			switch l.Type {
			case repo.Add:
				sign = "+"
			case repo.Del:
				sign = "-"
			}
			b.WriteString(sign + l.Text + "\n")
		}
	}
	return b.String()
}

// ---------- the Questions panel ----------

func (a *App) questionsPanel(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Grow(1).FillHeight().Children(func() {
		ui.Row(c).PaddingX(12).PaddingY(6).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, a.agentTitle).FontSize(12).Bold()
			ui.Text(c, a.tx.ReadOnly).FontSize(11).TextColor(t.TextMuted)
			ui.Spacer(c)
			if ui.Button(c.Key("new-question"), a.tx.NewQuestion).Clicked() {
				a.askChange()
			}
		})
		ui.Divider(c)
		threads := a.viewThreads()
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).Padding(12).Gap(14).FillWidth().Children(func() {
				if len(threads) == 0 {
					ui.Text(c, a.tx.NoQuestions).FontSize(13).TextColor(t.TextMuted)
				}
				for i := len(threads) - 1; i >= 0; i-- {
					a.threadCard(c.Key(fmt.Sprintf("thread-%d", threads[i].id)), threads[i])
				}
			})
		})
	})
}

func (a *App) threadCard(c *ui.Context, th *thread) {
	t := c.Theme()
	card := ui.Column(c).Gap(8).Padding(12).Radius(8).Border(1, t.Border).FillWidth()
	if th.focus {
		card.ScrollIntoView()
	}
	card.Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			label := ui.Text(c, th.label).FontSize(12).Bold().Grow(1).Ellipsis("…")
			if th.kind == threadLine {
				label.Underline().Cursor(ui.CursorPointer)
				if label.Clicked() {
					a.openFile(th.path, th.start, th.end)
				}
			}
			if ui.Text(c, "×").FontSize(12).TextColor(t.TextMuted).Cursor(ui.CursorPointer).Tooltip(a.tx.Delete).Clicked() {
				a.deleteThread(th)
			}
		})
		for i, m := range th.msgs {
			cc := c.Key(fmt.Sprintf("msg-%d", i))
			switch {
			case m.user:
				ui.Text(cc, m.text).FontSize(13).Padding(8).Radius(6).Background(t.Surface).FillWidth()
			default:
				streaming := th.busy && i == len(th.msgs)-1
				if streaming && m.text == "" {
					status := fmt.Sprintf(a.tx.Thinking, a.agentTitle)
					if n := len(m.tools); n > 0 {
						status = m.tools[n-1]
					}
					ui.Text(cc, status).FontSize(12).TextColor(t.TextMuted)
				}
				if m.text != "" {
					if streaming {
						markdown.Stream(cc, m.text, &a.md).FillWidth()
					} else {
						markdown.View(cc, m.text, &a.md).FillWidth()
					}
				}
				if m.err != "" {
					ui.Text(cc.Key("err"), m.err).FontSize(12).TextColor(t.Danger).FillWidth()
				}
			}
		}
		input := ui.TextArea(c.Key("draft"), &th.draft).Placeholder(a.tx.AskPlaceholder).FillWidth().Height(64)
		if th.focus {
			input.Focus()
			th.focus = false
		}
		send := false
		if input.Focused() && c.Shortcut(ui.Cmd, ui.KeyEnter) {
			send = true
		}
		ui.Row(c).Gap(8).Children(func() {
			ui.Spacer(c)
			if th.busy {
				if ui.Button(c.Key("stop"), a.tx.Stop).Clicked() && th.cancel != nil {
					th.cancel()
				}
			}
			if ui.Button(c.Key("send"), a.tx.Send).Disabled(th.busy || strings.TrimSpace(th.draft) == "").Clicked() {
				send = true
			}
		})
		if send {
			a.send(th)
		}
	})
}

// ---------- asking from diffs ----------

// lineMap turns a diff view's lines, from 0, into file lines, from 1.
type lineMap struct{ old, new []int }

func (m *lineMap) file(side editor.DiffSide, line int) int {
	if m == nil {
		return line + 1 // a whole file: its own numbering
	}
	s := m.new
	if side == editor.DiffOld {
		s = m.old
	}
	if line >= 0 && line < len(s) {
		return s[line]
	}
	return 0
}

func (m *lineMap) view(side editor.DiffSide, fileLine int) (int, bool) {
	if m == nil {
		return fileLine - 1, fileLine > 0
	}
	s := m.new
	if side == editor.DiffOld {
		s = m.old
	}
	for i, n := range s {
		if n == fileLine {
			return i, true
		}
	}
	return 0, false
}

// wireDiff lets the lines of a diff of path be asked about (the + by a line
// number, or a selection of lines), and marks the lines that have threads.
func (a *App) wireDiff(d *editor.DiffState, path string, m *lineMap) {
	d.LineSelection = true
	d.OnGutterButton = func(r editor.DiffLineRange) {
		if sel, ok := d.SelectedLines(); ok && sel.Side == r.Side && sel.Start <= r.Start && r.Start <= sel.End {
			r = sel
		}
		start, end := m.file(r.Side, r.Start), m.file(r.Side, r.End)
		if start == 0 {
			return
		}
		if end < start {
			end = start
		}
		a.askLines(path, r.Side, start, end)
	}
	d.RenderAnnotation = func(c *ui.Context, an editor.DiffAnnotation) {
		t := c.Theme()
		th := a.threadByID(an.Key)
		if th == nil {
			return
		}
		row := ui.Row(c).Gap(6).AlignItems(ui.Center).PaddingX(12).Height(an.Height).Cursor(ui.CursorPointer).Background(t.Surface).FillWidth()
		if row.Clicked() {
			a.focusThread(th)
		}
		row.Children(func() {
			n := 0
			for _, msg := range th.msgs {
				if msg.user {
					n++
				}
			}
			ui.Textf(c, "💬 %s · %d", th.label, n).FontSize(12).TextColor(t.TextMuted)
		})
	}
}

// annotate marks the threads on lines of path in d.
func (a *App) annotate(d *editor.DiffState, path string, m *lineMap) {
	var anns []editor.DiffAnnotation
	for _, th := range a.viewThreads() {
		if th.kind != threadLine || th.path != path {
			continue
		}
		if line, ok := m.view(th.side, th.end); ok {
			anns = append(anns, editor.DiffAnnotation{Side: th.side, Line: line, Height: 28, Key: th.id})
		}
	}
	d.Annotations = anns
}

func (a *App) threadByID(key any) *thread {
	id, _ := key.(int)
	for _, th := range a.threads {
		if th.id == id {
			return th
		}
	}
	return nil
}
