//go:build !windows

package monomind

import (
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestStartDetachedOwnGroup(t *testing.T) {
	cmd, err := startDetached(exec.Command("sleep", "5"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if pgid != cmd.Process.Pid {
		t.Fatalf("detached child pgid = %d, want its own pid %d", pgid, cmd.Process.Pid)
	}
}

func TestStartProcessGroupRunsAndKillTreeEndsIt(t *testing.T) {
	cmd := exec.Command("sleep", "5")
	setProcessGroup(cmd)
	release, err := startProcessGroup(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	KillProcessTree(cmd)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("KillProcessTree did not end the child")
	}
}

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
