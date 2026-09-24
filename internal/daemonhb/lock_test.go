package daemonhb

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// One daemon at a time: a second Lock fails while the first is held, and
// succeeds once it is released.
func TestLockIsSingleInstance(t *testing.T) {
	t.Setenv("MONOAGENT_DAEMON_HEARTBEAT", filepath.Join(t.TempDir(), "hb.json"))
	release, err := Lock()
	if err != nil {
		t.Fatal(err)
	}
	if !Locked() {
		t.Fatal("Locked() = false while held")
	}
	if _, err := Lock(); !errors.Is(err, ErrLocked) {
		t.Fatalf("second Lock = %v, want ErrLocked", err)
	}
	release()
	if Locked() {
		t.Fatal("Locked() = true after release")
	}
	again, err := Lock()
	if err != nil {
		t.Fatalf("Lock after release: %v", err)
	}
	again()
}

// The lock holds across processes, which is the case that matters: a
// second `monoagentcli daemon` is another process.
func TestLockAcrossProcesses(t *testing.T) {
	if os.Getenv("DAEMONHB_LOCK_HELPER") == "1" {
		if _, err := Lock(); errors.Is(err, ErrLocked) {
			os.Exit(3)
		}
		os.Exit(0)
	}
	t.Setenv("MONOAGENT_DAEMON_HEARTBEAT", filepath.Join(t.TempDir(), "hb.json"))
	release, err := Lock()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	cmd := exec.Command(os.Args[0], "-test.run", "^TestLockAcrossProcesses$")
	cmd.Env = append(os.Environ(), "DAEMONHB_LOCK_HELPER=1")
	err = cmd.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 3 {
		t.Fatalf("another process got the lock (err %v)", err)
	}
}
