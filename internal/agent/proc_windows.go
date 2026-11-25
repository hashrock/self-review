//go:build windows

package agent

import (
	"os"
	"syscall"
)

func ownGroup() *syscall.SysProcAttr { return nil }

func killGroup(p *os.Process) { _ = p.Kill() }
