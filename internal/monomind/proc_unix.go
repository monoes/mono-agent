//go:build !windows

package monomind

import (
	"os/exec"
	"syscall"
)

// setProcessGroup puts the child in its own process group so a group kill
// reaps monomind AND any agent-CLI grandchildren it spawned.
func setProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// killProcessGroup escalates SIGTERM → SIGKILL against the child's whole
// process group. pgid 0 means the child never started; nothing to do.
func killProcessGroup(cmd *exec.Cmd, pgid int) {
	if pgid == 0 && cmd.Process != nil {
		pgid = cmd.Process.Pid
	}
	if pgid == 0 {
		return
	}
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
}

// attachProcessGroup is called right after cmd.Start for a child this process
// will kill with killProcessGroup. Setpgid already made the group, so there
// is nothing to attach on unix; the returned release is a no-op.
func attachProcessGroup(*exec.Cmd) (release func()) { return func() {} }
