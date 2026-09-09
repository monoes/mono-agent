//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

// hideWindow configures cmd so that no visible console window is created on Windows.
func hideWindow(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= 0x08000000 // CREATE_NO_WINDOW
}

// setChatProcessGroup configures the chat subprocess to run without showing a console window on Windows.
func setChatProcessGroup(cmd *exec.Cmd) {
	hideWindow(cmd)
}

// killChatProcessGroup kills the direct child only on Windows.
func killChatProcessGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

// readProcessCommandLine is a stub on Windows where /proc and ps are unavailable.
func readProcessCommandLine(pid int) (cmdline string, alive bool, err error) {
	return "", false, nil
}

// signalWorkflowPID is a no-op on Windows: POSIX signals are not supported
// (Process.Signal always errors), so the external-PID cancel path was already
// ineffective there — no pid verification needed (RA1-8).
func signalWorkflowPID(pid int) error {
	return nil
}
