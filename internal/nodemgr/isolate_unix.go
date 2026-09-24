//go:build !windows

package nodemgr

import (
	"os/exec"
	"syscall"
)

// detach starts cmd in a new session, which also makes it a process group
// leader, so proctree.Kill reaches everything it starts.
func detach(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setsid = true
}
