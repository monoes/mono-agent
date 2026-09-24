//go:build !windows

package nodemgr

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// An installer that asks on the terminal fails at once instead of waiting
// for an answer nobody will type: it has no controlling terminal.
func TestStreamCmdHasNoTerminal(t *testing.T) {
	start := time.Now()
	err := StreamCmd(exec.Command("sh", "-c", "read answer < /dev/tty; echo got $answer"), func(string) {})
	if err == nil {
		t.Fatal("a command reading /dev/tty succeeded; it must have no terminal")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("took %v; a prompt must fail at once", d)
	}
}

// Cancelling an install ends everything it started, not only the direct
// child (a vendor script's own subprocesses).
func TestStreamCmdCancelKillsTheTree(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var pid int
	got := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- StreamCmd(exec.CommandContext(ctx, "sh", "-c", "sleep 60 & echo $!; wait"), func(line string) {
			if n, err := strconv.Atoi(strings.TrimSpace(line)); err == nil && pid == 0 {
				pid = n
				close(got)
			}
		})
	}()
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("the command never started its child")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("StreamCmd did not return after cancel")
	}
	deadline := time.Now().Add(3 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("grandchild %d survived the cancel", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
