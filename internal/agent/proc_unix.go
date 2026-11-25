//go:build !windows

package agent

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// ownGroup starts the agent in a process group of its own, so Close ends
// what it starts too (npx starts node, which starts the agent's CLI).
func ownGroup() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setpgid: true} }

func killGroup(p *os.Process) {
	_ = syscall.Kill(-p.Pid, syscall.SIGTERM)
	time.AfterFunc(2*time.Second, func() { _ = syscall.Kill(-p.Pid, syscall.SIGKILL) })
}

// shellPath asks the user's login shell for its PATH; "" when it cannot.
func shellPath() string {
	sh := os.Getenv("SHELL")
	if sh == "" {
		sh = "/bin/zsh"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// -i too: version managers such as mise and nvm hook into the
	// interactive startup files.
	out, err := exec.CommandContext(ctx, sh, "-ilc", `printf '__PATH__%s' "$PATH"`).Output()
	if err != nil {
		return ""
	}
	s := string(out)
	i := strings.LastIndex(s, "__PATH__")
	if i < 0 {
		return ""
	}
	return strings.TrimSpace(s[i+len("__PATH__"):])
}
