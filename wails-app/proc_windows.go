//go:build windows

package main

import (
	"os/exec"
)

// setChatProcessGroup is a no-op pending the dedicated Windows pass.
func setChatProcessGroup(cmd *exec.Cmd) {}

// killChatProcessGroup kills the direct child only on Windows.
func killChatProcessGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

// signalWorkflowPID is a no-op on Windows: POSIX signals are not supported
// (Process.Signal always errors), so the external-PID cancel path was already
// ineffective there — no pid verification needed (RA1-8).
func signalWorkflowPID(pid int) error {
	return nil
}
