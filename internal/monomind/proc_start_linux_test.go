//go:build linux

package monomind

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestStartProcessGroupSetsParentDeathSignal(t *testing.T) {
	cmd := exec.Command("sleep", "5")
	setProcessGroup(cmd)
	release, err := startProcessGroup(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	defer func() { killProcessGroup(cmd, 0); _ = cmd.Wait() }()
	if got := cmd.SysProcAttr.Pdeathsig; got != syscall.SIGTERM {
		t.Fatalf("Pdeathsig = %v, want SIGTERM", got)
	}
	if !cmd.SysProcAttr.Setpgid {
		t.Fatal("startProcessGroup dropped Setpgid")
	}
}

// helperFollowerEnv makes this test binary act as monoagentcli running
// `org events --follow` (see TestOrgEventsFollowerDiesWithParent).
const helperFollowerEnv = "MONOMIND_TEST_FOLLOWER_PARENT"

func TestHelperFollowerParent(t *testing.T) {
	if os.Getenv(helperFollowerEnv) == "" {
		t.Skip("helper process for TestOrgEventsFollowerDiesWithParent")
	}
	_ = OrgEvents(context.Background(), ".", "growth", OrgEventsOptions{Follow: true}, func([]byte) {})
}

// Issue #235: a monoagentcli that dies without cancelling (the app used to
// SIGKILL it on quit) must not leave its `monomind org events --follow`
// child running forever.
func TestOrgEventsFollowerDiesWithParent(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")
	bin := filepath.Join(dir, "follow-monomind.sh")
	script := fmt.Sprintf(`#!/bin/sh
if [ "$1" = "--version" ] && [ "$2" = "--json" ]; then
  echo '{"v":1,"version":"2.10.0","min_caller":"1.0.0","capabilities":["agent-exec","agent-scan","org-json-v1"]}'
  exit 0
fi
echo $$ > %q.tmp && mv %q.tmp %q
exec sleep 60
`, pidFile, pidFile, pidFile)
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	parent := exec.Command(os.Args[0], "-test.run=^TestHelperFollowerParent$")
	parent.Env = append(os.Environ(), helperFollowerEnv+"=1", EnvOverride+"="+bin, "TMPDIR="+dir)
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	parentDone := make(chan struct{})
	go func() { _ = parent.Wait(); close(parentDone) }()
	defer func() { _ = parent.Process.Kill(); <-parentDone }()

	var child int
	deadline := time.Now().Add(10 * time.Second)
	for child == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the follower never started")
		}
		if b, err := os.ReadFile(pidFile); err == nil {
			child, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		}
		time.Sleep(20 * time.Millisecond)
	}

	if err := parent.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	<-parentDone

	deadline = time.Now().Add(5 * time.Second)
	for processRunning(child) {
		if time.Now().After(deadline) {
			_ = syscall.Kill(child, syscall.SIGKILL)
			t.Fatalf("follower %d outlived its SIGKILLed parent", child)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// processRunning reports whether pid exists and is not a zombie waiting for
// its new parent to reap it.
func processRunning(pid int) bool {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	// The state follows the parenthesised command name.
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	return i < 0 || i+2 >= len(s) || s[i+2] != 'Z'
}
