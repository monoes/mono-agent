//go:build !windows

package main

import (
	"context"
	"errors"
	"os/exec"
	"sync"
	"syscall"
	"testing"
	"time"
)

// startGroup starts script (sh -c) in its own process group, as a chat
// turn is started, and returns a channel that yields its Wait error.
func startGroup(t *testing.T, ctx context.Context, script string) (*exec.Cmd, <-chan error) {
	t.Helper()
	cmd := exec.CommandContext(ctx, "sh", "-c", script)
	setChatProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- waitChatProcess(cmd) }()
	t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })
	return cmd, done
}

func withGrace(t *testing.T, d time.Duration) {
	t.Helper()
	prev := chatKillGrace
	chatKillGrace = d
	t.Cleanup(func() { chatKillGrace = prev })
}

func exitSignal(err error) syscall.Signal {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return ws.Signal()
		}
	}
	return 0
}

// A process that honors SIGTERM ends from it: no SIGKILL first.
func TestKillChatProcessGroup_SIGTERMFirst(t *testing.T) {
	withGrace(t, 5*time.Second)
	cmd, done := startGroup(t, context.Background(), "sleep 30")
	killChatProcessGroup(cmd)
	select {
	case err := <-done:
		if sig := exitSignal(err); sig != syscall.SIGTERM {
			t.Errorf("exit = %v (signal %v), want SIGTERM", err, sig)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("process did not exit on SIGTERM")
	}
}

// A group that ignores SIGTERM survives the grace period, then is SIGKILLed;
// the call itself never blocks.
func TestKillChatProcessGroup_SIGKILLAfterGrace(t *testing.T) {
	withGrace(t, 600*time.Millisecond)
	cmd, done := startGroup(t, context.Background(), `trap "" TERM; sleep 30 & wait; wait`)
	time.Sleep(200 * time.Millisecond) // let sh install its trap
	start := time.Now()
	killChatProcessGroup(cmd)
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Errorf("killChatProcessGroup blocked for %s", d)
	}
	select {
	case err := <-done:
		t.Fatalf("exited before the grace period: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	select {
	case err := <-done:
		if sig := exitSignal(err); sig != syscall.SIGKILL {
			t.Errorf("exit = %v (signal %v), want SIGKILL", err, sig)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("process group was not SIGKILLed after the grace period")
	}
}

// Cancelling the turn's ctx (the supervisor does, before Kill) must be the
// same graceful stop, not exec's default immediate SIGKILL of the child.
func TestSetChatProcessGroup_CtxCancelIsGraceful(t *testing.T) {
	withGrace(t, 5*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	_, done := startGroup(t, ctx, "sleep 30")
	cancel()
	select {
	case err := <-done:
		if sig := exitSignal(err); sig != syscall.SIGTERM {
			t.Errorf("exit = %v (signal %v), want SIGTERM", err, sig)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("process did not exit after ctx cancel")
	}
}

// Killing a group that already exited is a harmless no-op.
func TestKillChatProcessGroup_AfterExit(t *testing.T) {
	cmd, done := startGroup(t, context.Background(), "exit 0")
	<-done
	killChatProcessGroup(cmd)
}

// Once Wait has reaped the leader its pid (the group id) may be reused, so
// the delayed SIGKILL must not fire, even for a group that ignored SIGTERM.
func TestKillChatProcessGroup_NoSIGKILLAfterReap(t *testing.T) {
	withGrace(t, 700*time.Millisecond)
	var mu sync.Mutex
	var sent []syscall.Signal
	prev := groupKill
	groupKill = func(pid int, sig syscall.Signal) error {
		mu.Lock()
		sent = append(sent, sig)
		mu.Unlock()
		return syscall.Kill(pid, sig)
	}
	t.Cleanup(func() { groupKill = prev })

	// Ignores SIGTERM but exits on its own well before the grace period.
	cmd, done := startGroup(t, context.Background(), `trap "" TERM; sleep 0.3`)
	time.Sleep(100 * time.Millisecond) // let sh install its trap
	killChatProcessGroup(cmd)
	select {
	case err := <-done:
		if sig := exitSignal(err); sig != 0 {
			t.Fatalf("exit = %v (signal %v), want a normal exit", err, sig)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("process did not exit")
	}
	time.Sleep(900 * time.Millisecond) // past the grace period
	mu.Lock()
	defer mu.Unlock()
	for _, sig := range sent {
		if sig == syscall.SIGKILL {
			t.Fatalf("signals sent = %v: SIGKILL after the leader was reaped", sent)
		}
	}
	if len(sent) != 1 || sent[0] != syscall.SIGTERM {
		t.Errorf("signals sent = %v, want just SIGTERM", sent)
	}
	if reapedChan(cmd) != nil {
		t.Error("reaped command is still tracked")
	}
}
