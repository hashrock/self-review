//go:build windows

package main

import "syscall"

const detachedProcess = 0x00000008

func detached() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: detachedProcess}
}
