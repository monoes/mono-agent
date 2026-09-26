//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// detachStart starts cmd in its own session so it outlives this process
// and the app that ran it.
func detachStart(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd.Start()
}
