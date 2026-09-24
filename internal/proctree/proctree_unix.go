//go:build !windows

package proctree

import (
	"os/exec"
	"syscall"
)

// Kill ends a started child and its descendants by signalling its process
// group, SIGTERM then SIGKILL; the child must have been started with
// Setpgid so that its pid is the group id. A no-op before Start.
func Kill(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pgid := cmd.Process.Pid
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
}
