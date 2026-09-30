//go:build !windows

package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/daemonhb"
)

// stopDaemonHelperEnv re-execs this test binary as a stand-in daemon: takes
// the lock, writes a heartbeat, and blocks until SIGTERM releases it —
// mirroring daemon.go's own signal-triggered graceful shutdown closely
// enough for stopDaemon to have something real to stop. See
// TestLockAcrossProcesses in internal/daemonhb for the same re-exec idiom.
// Unix-only: terminateProcess/killProcess are stubbed out on Windows (see
// coder_signal_windows.go), so stopDaemon can't reach this path there.
const stopDaemonHelperEnv = "MONOAGENTCLI_STOPDAEMON_HELPER"

func TestStopDaemonSignalsAndWaitsForTheLockToClear(t *testing.T) {
	if os.Getenv(stopDaemonHelperEnv) == "1" {
		release, err := daemonhb.Lock()
		if err != nil {
			os.Exit(1)
		}
		if err := daemonhb.Write(daemonhb.Heartbeat{PID: os.Getpid()}); err != nil {
			os.Exit(1)
		}
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGTERM)
		<-sig
		release()
		os.Exit(0)
	}

	hbPath := filepath.Join(t.TempDir(), "hb.json")
	t.Setenv("MONOAGENT_DAEMON_HEARTBEAT", hbPath)

	cmd := exec.Command(os.Args[0], "-test.run", "^TestStopDaemonSignalsAndWaitsForTheLockToClear$")
	cmd.Env = append(os.Environ(), stopDaemonHelperEnv+"=1", "MONOAGENT_DAEMON_HEARTBEAT="+hbPath)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() { _ = cmd.Process.Kill() }) // in case the test fails before stopDaemon reaps it

	deadline := time.Now().Add(5 * time.Second)
	for !daemonhb.Locked() {
		if time.Now().After(deadline) {
			t.Fatal("helper never took the lock")
		}
		time.Sleep(20 * time.Millisecond)
	}

	var progressLines []string
	err := stopDaemon(context.Background(), pid, func(line string) { progressLines = append(progressLines, line) })
	if err != nil {
		t.Fatalf("stopDaemon: %v", err)
	}
	if daemonhb.Locked() {
		t.Fatal("lock still held after stopDaemon returned")
	}
	if len(progressLines) == 0 {
		t.Error("expected at least one progress line")
	}

	waitErr := cmd.Wait()
	var exitErr *exec.ExitError
	if waitErr != nil && (!errors.As(waitErr, &exitErr) || exitErr.ExitCode() != 0) {
		t.Errorf("helper process exit: %v", waitErr)
	}
}

func TestStopDaemonNoopWhenAlreadyStopped(t *testing.T) {
	t.Setenv("MONOAGENT_DAEMON_HEARTBEAT", filepath.Join(t.TempDir(), "hb.json"))
	called := false
	err := stopDaemon(context.Background(), 999999, func(string) { called = true })
	if err != nil {
		t.Fatalf("stopDaemon on an already-clear lock: %v", err)
	}
	if called {
		t.Error("no progress expected when there was nothing to stop")
	}
}
