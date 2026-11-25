//go:build !windows

package main

import "syscall"

// detached starts the window in its own session, so it outlives the shell
// (or agent) that ran self-review open.
func detached() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }
