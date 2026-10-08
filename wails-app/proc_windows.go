//go:build windows

package main

import (
	"os/exec"
	"syscall"
	"time"

	"github.com/monoes/mono-agent/internal/proctree"
)

// chatKillGrace mirrors proc_unix.go's: stopRunningCmds waits up to it
// (plus shutdownReapMargin) for reaped process groups. Windows tracks no
// group, so nothing waits on it here; it must still exist for app.go.
var chatKillGrace = 15 * time.Second

// createNoWindow is the CREATE_NO_WINDOW process-creation flag.
const createNoWindow = 0x08000000

// suppressConsole keeps cmd from opening a console window when it starts.
func suppressConsole(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	attr := cmd.SysProcAttr
	if attr == nil {
		attr = new(syscall.SysProcAttr)
		cmd.SysProcAttr = attr
	}
	attr.HideWindow = true
	attr.CreationFlags |= createNoWindow
}

// setChatProcessGroup prepares the chat subprocess: no console window. A
// command built with exec.CommandContext also gets its cancel hook swapped
// for a full tree kill, because the default only kills the direct child and
// the chat supervisor cancels the turn's ctx before calling Kill. Once
// monoagentcli is gone taskkill /T can no longer find the monomind and
// agent-CLI processes under it.
func setChatProcessGroup(cmd *exec.Cmd) {
	suppressConsole(cmd)
	if cmd.Cancel == nil {
		return
	}
	cmd.Cancel = func() error {
		killChatProcessGroup(cmd)
		return nil
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
