//go:build !windows

package main

import (
	"os/exec"
	"syscall"
	"time"
)

// hideWindow is a no-op on non-Windows platforms.
func hideWindow(cmd *exec.Cmd) {}

// setChatProcessGroup isolates the chat subprocess (monoagentcli → monomind
// → agent CLI) in its own process group so a UI stop reaps the whole tree.
// For a command made with exec.CommandContext it also replaces the default
// ctx cancel, an immediate SIGKILL of the direct child: the chat supervisor
// cancels the turn's ctx before it calls Kill, and a SIGKILLed monoagentcli
// can't cancel its run, which would orphan the agent under it.
func setChatProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	trackChatGroup(cmd)
	if cmd.Cancel != nil {
		cmd.Cancel = func() error {
			killChatProcessGroup(cmd)
			return nil
		}
	}
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

// chatKillGrace is how long a SIGTERMed chat process group gets to exit
// before it is SIGKILLed. monoagentcli turns SIGTERM into a cancel, and
// monomind then ends the agent's whole process tree itself (about 6s).
// A variable so tests can shorten it.
var chatKillGrace = 15 * time.Second

// groupKill signals a process group; a variable so tests can watch it.
var groupKill = syscall.Kill

// killChatProcessGroup SIGTERMs the whole chat process group, then SIGKILLs
// it after chatKillGrace unless its leader has been reaped by then. It
// returns at once; the SIGKILL runs on a timer.
//
// Reaped means nothing is left to kill and the group id may be reused: the
// group only ever holds monoagentcli itself (it starts monomind in its own
// group), and once Wait has reaped the leader its pid is free again. So
// the timer only fires while waitChatProcess hasn't returned. A command
// that isn't tracked (reaped before the stop, or never set up with
// setChatProcessGroup) gets the SIGTERM only.
func killChatProcessGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	pgid := cmd.Process.Pid
	reaped := reapedChan(cmd)
	if err := groupKill(-pgid, syscall.SIGTERM); err == syscall.ESRCH || reaped == nil {
		return
	}
	time.AfterFunc(chatKillGrace, func() {
		select {
		case <-reaped:
		default:
			_ = groupKill(-pgid, syscall.SIGKILL)
		}
	})
}
