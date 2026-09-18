//go:build !windows

package monomind

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/daemonhb"
)

func writeServeHeartbeat(t *testing.T, root string, pid int, at time.Time) {
	t.Helper()
	dir := filepath.Join(root, ".monomind")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(ServeHeartbeat{PID: pid, UpdatedAt: at})
	if err := os.WriteFile(filepath.Join(dir, "serve-heartbeat.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// startFakeServe stands in for `monomind org serve` the way OrgServeStart
// launches it: its own process group, with a grandchild in that group.
func startFakeServe(t *testing.T, script string) (*exec.Cmd, chan struct{}) {
	t.Helper()
	cmd := exec.Command("sh", "-c", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })
	return cmd, done
}

// C-24: deleting a profile or moving its folder must stop the folder's
// `org serve`, or it keeps running orgs out of a folder the profile no
// longer owns.
func TestOrgServeStopTerminatesTheServeProcessGroup(t *testing.T) {
	root := t.TempDir()
	cmd, done := startFakeServe(t, "sleep 60 & wait")
	writeServeHeartbeat(t, root, cmd.Process.Pid, time.Now())

	pid, stopped, err := OrgServeStop(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if !stopped || pid != cmd.Process.Pid {
		t.Fatalf("OrgServeStop = pid %d stopped %v, want pid %d stopped", pid, stopped, cmd.Process.Pid)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("serve process still running")
	}
	if err := syscall.Kill(-cmd.Process.Pid, 0); err == nil {
		t.Fatal("a process of the serve group survived")
	}
	if _, live := ReadServeHeartbeat(root); live {
		t.Fatal("heartbeat still reads live after stop")
	}
}

// A serve daemon that ignores SIGTERM is killed once the grace runs out.
func TestOrgServeStopEscalatesToKill(t *testing.T) {
	old := serveStopGrace
	serveStopGrace = 300 * time.Millisecond
	t.Cleanup(func() { serveStopGrace = old })

	root := t.TempDir()
	cmd, done := startFakeServe(t, "trap '' TERM; while :; do sleep 0.05; done")
	time.Sleep(100 * time.Millisecond) // let the trap install
	writeServeHeartbeat(t, root, cmd.Process.Pid, time.Now())

	if _, stopped, err := OrgServeStop(context.Background(), root); err != nil || !stopped {
		t.Fatalf("OrgServeStop = stopped %v, err %v", stopped, err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("serve process survived SIGKILL escalation")
	}
}

func TestOrgServeStopWithNothingRunning(t *testing.T) {
	root := t.TempDir()
	if pid, stopped, err := OrgServeStop(context.Background(), root); err != nil || stopped || pid != 0 {
		t.Fatalf("no heartbeat: pid %d stopped %v err %v", pid, stopped, err)
	}
	// A stale heartbeat naming a live pid (ours) must not get that pid
	// signalled: the pid may since belong to an unrelated process.
	writeServeHeartbeat(t, root, os.Getpid(), time.Now().Add(-10*time.Minute))
	if _, stopped, err := OrgServeStop(context.Background(), root); err != nil || stopped {
		t.Fatalf("stale heartbeat: stopped %v err %v", stopped, err)
	}
	if !daemonhb.ProcessAlive(os.Getpid()) {
		t.Fatal("unreachable")
	}
}
