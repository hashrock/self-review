//go:build windows

package agent

import (
	"os"
	"syscall"
)

func ownGroup() *syscall.SysProcAttr { return nil }

func killGroup(p *os.Process) { _ = p.Kill() }

// shellPath keeps the environment's PATH on Windows.
func shellPath() string { return "" }
