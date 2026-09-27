//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

const (
	detachedProcess       = 0x00000008
	createNewProcessGroup = 0x00000200
	createNoWindow        = 0x08000000
)

// detachStart starts cmd detached from this console, without a window, so
// it outlives this process and the app that ran it.
func detachStart(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: detachedProcess | createNewProcessGroup | createNoWindow}
	return cmd.Start()
}
