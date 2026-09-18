//go:build !windows

package monomind

import (
	"syscall"
	"testing"
	"time"
)

// assertGroupReaped fails when any of pids is still alive 3 seconds after a
// group kill, and SIGKILLs the survivors.
func assertGroupReaped(t *testing.T, pids []int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		alive := 0
		for _, pid := range pids {
			if err := syscall.Kill(pid, 0); err == nil {
				alive++
			}
		}
		if alive == 0 {
			return // success — group fully reaped
		}
		time.Sleep(100 * time.Millisecond)
	}
	for _, pid := range pids {
		if err := syscall.Kill(pid, 0); err == nil {
			t.Errorf("orphan process %d survived the group kill", pid)
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
}
