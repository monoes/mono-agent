//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// hideWindow is a no-op on non-Windows platforms.
func hideWindow(cmd *exec.Cmd) {}

// setChatProcessGroup isolates the chat subprocess (monoagentcli → monomind
// → agent CLI) in its own process group so a UI stop reaps the whole tree.
func setChatProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// terminateCLI asks a monoagentcli child to stop with SIGTERM, which the
// CLI turns into a cancelled context: it then ends what it started itself
// (installers run in their own session, out of reach of a group kill).
// exec.Cmd's WaitDelay kills it if it doesn't exit in time.
func terminateCLI(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Signal(syscall.SIGTERM)
}

// killChatProcessGroup SIGTERMs then SIGKILLs the whole chat process group.
func killChatProcessGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	pgid := cmd.Process.Pid
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
}
