//go:build windows

package main

import (
	"os/exec"
	"syscall"

	"github.com/monoes/mono-agent/internal/proctree"
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

// setChatProcessGroup configures the chat subprocess to run without showing
// a console window on Windows. For a command made with exec.CommandContext
// it also replaces the default ctx cancel, which kills only the direct
// child: the chat supervisor cancels the turn's ctx before it calls Kill,
// and once monoagentcli is gone taskkill /T can no longer find the
// monomind and agent-CLI processes under it.
func setChatProcessGroup(cmd *exec.Cmd) {
	hideWindow(cmd)
	if cmd.Cancel != nil {
		cmd.Cancel = func() error {
			killChatProcessGroup(cmd)
			return nil
		}
	}
}

// terminateCLI ends a monoagentcli child and its tree: Windows has no
// SIGTERM, so this is the same tree kill as for the chat.
func terminateCLI(cmd *exec.Cmd) error {
	killChatProcessGroup(cmd)
	return nil
}

// killChatProcessGroup kills the chat subprocess and everything under it
// (monoagentcli → monomind → agent CLI) with internal/proctree, which the
// CLI also uses for its own children; the GUI does not import
// internal/monomind (see frontend/src/doctrine.test.js). The GUI does not
// put the chat in a Job Object, so this is taskkill /T /F by parent pid,
// then the direct child. It is safe to call after the child has exited.
func killChatProcessGroup(cmd *exec.Cmd) {
	proctree.Kill(cmd)
}
