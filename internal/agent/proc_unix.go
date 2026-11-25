//go:build !windows

package agent

import (
	"os"
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
