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

// The stand-in daemon is this test binary run again (see TestLockAcrossProcesses in
// internal/daemonhb for the same idiom): stopDaemonHelperEnv names what it plays, and
// TestStopDaemonHelperProcess is the entry point the parent runs it through. Unix-only:
// terminateProcess and killProcess are stubs on Windows (coder_signal_windows.go), so
// stopDaemon cannot reach this path there.
const stopDaemonHelperEnv = "MONOAGENTCLI_STOPDAEMON_HELPER"

// runStopDaemonHelper plays the daemon of the "lock" mode: it takes the single-instance lock,
// writes its heartbeat, and on SIGTERM removes the heartbeat (the real daemon's heartbeat
// goroutine does that as soon as its context ends) and exits. It listens for SIGTERM before it
// takes the lock: a signal sent the moment the lock becomes visible must find a handler.
func runStopDaemonHelper() {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM)
	release, err := daemonhb.Lock()
	if err != nil {
		os.Exit(1)
	}
	defer release()
	if err := daemonhb.Write(daemonhb.Heartbeat{PID: os.Getpid()}); err != nil {
		os.Exit(1)
	}
	<-sig
	_ = os.Remove(daemonhb.Path())
}

// TestStopDaemonHelperProcess is not a test: it is the entry point of the stand-in daemon.
// It returns (and does not os.Exit) so that TestMain removes the temporary home it made.
func TestStopDaemonHelperProcess(t *testing.T) {
	if os.Getenv(stopDaemonHelperEnv) == "" {
		t.Skip("entry point of the stand-in daemon, run by the stopDaemon tests")
	}
	runStopDaemonHelper()
}

// startStopDaemonHelper starts the stand-in daemon and returns once this machine's heartbeat
// names it, which it writes only after it holds the lock (the parent never probes the lock
// meanwhile: a probe takes it for an instant and could make the helper's own attempt fail).
// It reaps the helper in the background, as a service manager would: a child nobody waits for
// stays a zombie that `kill -0` still finds.
func startStopDaemonHelper(t *testing.T) (pid int, exited <-chan error) {
	t.Helper()
	hbPath := filepath.Join(t.TempDir(), "hb.json")
	t.Setenv("MONOAGENT_DAEMON_HEARTBEAT", hbPath)

	cmd := exec.Command(os.Args[0], "-test.run=^TestStopDaemonHelperProcess$")
	cmd.Env = append(os.Environ(), stopDaemonHelperEnv+"=lock", "MONOAGENT_DAEMON_HEARTBEAT="+hbPath)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() }) // in case the test fails before stopDaemon stops it
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	deadline := time.Now().Add(10 * time.Second)
	for {
		if hb, ok := daemonhb.Read(); ok && hb.PID == cmd.Process.Pid {
			return cmd.Process.Pid, done
		}
		if time.Now().After(deadline) {
			t.Fatal("the helper never became the daemon")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestStopDaemonSignalsAndWaitsForTheLockToClear(t *testing.T) {
	pid, exited := startStopDaemonHelper(t)

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

	waitErr := <-exited
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
