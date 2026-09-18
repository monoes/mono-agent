//go:build !windows

package daemonhb

import "syscall"

// ProcessAlive reports whether a process with the given pid is currently running.
// Signal 0 probes for existence without affecting the target; EPERM means the
// process exists but is owned by another user.
func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
