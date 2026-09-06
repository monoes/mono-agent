package monomind

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestOrgListAgainstFake(t *testing.T) {
	os.Setenv(EnvOverride, fakeBin(t, "fake-monomind.sh"))
	defer os.Unsetenv(EnvOverride)

	out, err := OrgList(context.Background(), ".")
	if err != nil {
		t.Fatalf("OrgList: %v", err)
	}
	if !strings.Contains(string(out), `"growth"`) {
		t.Errorf("OrgList output = %s, want it to contain the fake org", out)
	}
}

func TestOrgStatusAgainstFake(t *testing.T) {
	os.Setenv(EnvOverride, fakeBin(t, "fake-monomind.sh"))
	defer os.Unsetenv(EnvOverride)

	out, err := OrgStatus(context.Background(), ".", "growth")
	if err != nil {
		t.Fatalf("OrgStatus: %v", err)
	}
	if !strings.Contains(string(out), `"running"`) {
		t.Errorf("OrgStatus output = %s, want status running", out)
	}
}

func TestOrgStatusErrorSurfacesStderr(t *testing.T) {
	os.Setenv(EnvOverride, fakeBin(t, "fake-monomind.sh"))
	defer os.Unsetenv(EnvOverride)

	_, err := OrgStatus(context.Background(), ".", "missing-org")
	if err == nil {
		t.Fatal("OrgStatus() = nil error, want an error for a nonexistent org")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("OrgStatus() error = %q, want it to surface the fake's stderr message", err.Error())
	}
}

func TestOrgEventsStreamsLines(t *testing.T) {
	os.Setenv(EnvOverride, fakeBin(t, "fake-monomind.sh"))
	defer os.Unsetenv(EnvOverride)

	var lines [][]byte
	err := OrgEvents(context.Background(), ".", "growth", OrgEventsOptions{}, func(line []byte) {
		cp := make([]byte, len(line))
		copy(cp, line)
		lines = append(lines, cp)
	})
	if err != nil {
		t.Fatalf("OrgEvents: %v", err)
	}
	if len(lines) != 2 {
		t.Fatalf("OrgEvents delivered %d lines, want 2", len(lines))
	}
	if !strings.Contains(string(lines[0]), `"e1"`) {
		t.Errorf("first line = %s, want it to contain event e1", lines[0])
	}
}

// TestOrgEventsAbortsPromptlyOnCancelDuringHandshake guards against OrgEvents
// hanging forever when the caller cancels before the subprocess even starts
// streaming: fake-monolith.sh never answers `--version --json`, so without
// ctx propagation through Ensure()'s handshake this would block until the
// script's internal 60s sleep, not return promptly.
// TestOrgRunCancelGroupKill is the regression test for the bug where
// OrgRun built its exec.Cmd with plain exec.CommandContext and no
// setProcessGroup call, unlike its siblings OrgRunStart and OrgEvents:
// OrgRun's own doc comment says it can block "minutes to hours" with the
// caller's ctx as the only deadline, but exec.CommandContext's default
// Cancel behavior only kills the DIRECT child on cancellation, not its
// process group — leaking any agent-CLI grandchild `monomind org run`
// spawned as an orphan. Mirrors TestExecCancelGroupKill's approach: spawn
// a fake monomind that forks a long-lived grandchild, cancel mid-flight,
// and assert zero survivors.
func TestOrgRunCancelGroupKill(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("group kill is unix-only in this build")
	}
	os.Setenv(EnvOverride, fakeBin(t, "fake-monolith-org.sh"))
	defer os.Unsetenv(EnvOverride)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		_, err := OrgRun(ctx, ".", "growth", "", false)
		errCh <- err
	}()

	// Wait for the fake org-run process (and its sleep grandchild) to exist.
	var pids []int
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && len(pids) < 2 {
		pids = pids[:0]
		out, _ := exec.Command("pgrep", "-f", "fake-monolith-org").Output()
		for _, s := range fields(string(out)) {
			pids = append(pids, atoi(s))
		}
		if len(pids) > 0 {
			sleepOut, _ := exec.Command("sh", "-c", "pgrep -P "+itoa(pids[0])).Output()
			for _, s := range fields(string(sleepOut)) {
				pids = append(pids, atoi(s))
			}
		}
		if len(pids) < 2 {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if len(pids) < 2 {
		t.Fatalf("expected fake org-run process + child, found pids %v", pids)
	}

	cancel()
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("OrgRun() = nil error after ctx cancellation, want ctx.Err()")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("OrgRun did not return after cancel")
	}

	// The gate: zero orphaned processes from the group.
	deadline = time.Now().Add(3 * time.Second)
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

func TestOrgEventsAbortsPromptlyOnCancelDuringHandshake(t *testing.T) {
	os.Setenv(EnvOverride, fakeBin(t, "fake-monolith.sh"))
	defer os.Unsetenv(EnvOverride)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan error, 1)
	go func() {
		done <- OrgEvents(ctx, ".", "growth", OrgEventsOptions{Follow: true}, func([]byte) {})
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Error("OrgEvents() = nil error with an already-cancelled ctx, want an error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("OrgEvents() did not return within 5s of ctx cancellation during handshake")
	}
}
