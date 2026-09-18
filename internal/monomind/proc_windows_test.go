//go:build windows

package monomind

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// procHelperEnv makes the test binary act as a helper process: "parent"
// starts a "child" copy of itself, prints the child's pid and sleeps;
// "child" just sleeps.
const procHelperEnv = "MONOMIND_PROC_TEST_HELPER"

func TestProcHelperProcess(t *testing.T) {
	switch os.Getenv(procHelperEnv) {
	case "parent":
		child := exec.Command(os.Args[0], "-test.run=^TestProcHelperProcess$")
		child.Env = append(os.Environ(), procHelperEnv+"=child")
		if err := child.Start(); err != nil {
			fmt.Println("error", err)
			os.Exit(1)
		}
		fmt.Println(child.Process.Pid)
		time.Sleep(time.Minute)
		os.Exit(0)
	case "child":
		time.Sleep(time.Minute)
		os.Exit(0)
	}
}

// startHelperTree starts parent → child and returns the parent's cmd and the
// child's pid.
func startHelperTree(t *testing.T, attach bool) (*exec.Cmd, int, func()) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestProcHelperProcess$")
	cmd.Env = append(os.Environ(), procHelperEnv+"=parent")
	setProcessGroup(cmd)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	release := func() {}
	if attach {
		release = attachProcessGroup(cmd)
	}
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatalf("reading child pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatalf("helper said %q", line)
	}
	return cmd, pid, release
}

// waitGone reports whether pid exits within the timeout.
func waitGone(t *testing.T, pid int, timeout time.Duration) bool {
	t.Helper()
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return true // already gone
	}
	defer windows.CloseHandle(h)
	ev, _ := windows.WaitForSingleObject(h, uint32(timeout/time.Millisecond))
	return ev == windows.WAIT_OBJECT_0
}

// assertGroupReaped backs the pgrep-based group-kill tests in exec_test.go
// and org_test.go, which skip on Windows before reaching it.
func assertGroupReaped(t *testing.T, pids []int) {
	t.Helper()
	for _, pid := range pids {
		if !waitGone(t, pid, 3*time.Second) {
			t.Errorf("orphan process %d survived the group kill", pid)
		}
	}
}

func TestKillProcessGroupKillsGrandchildViaJob(t *testing.T) {
	cmd, childPid, release := startHelperTree(t, true)
	defer release()
	if _, ok := jobs.Load(cmd); !ok {
		t.Fatal("attachProcessGroup did not record a job")
	}
	killProcessGroup(cmd, cmd.Process.Pid)
	_ = cmd.Wait()
	if !waitGone(t, childPid, 10*time.Second) {
		t.Fatalf("grandchild %d survived the group kill", childPid)
	}
}

func TestKillProcessGroupTaskkillFallback(t *testing.T) {
	cmd, childPid, _ := startHelperTree(t, false)
	killProcessGroup(cmd, cmd.Process.Pid)
	_ = cmd.Wait()
	if !waitGone(t, childPid, 10*time.Second) {
		t.Fatalf("grandchild %d survived taskkill /T", childPid)
	}
}

func TestReleaseLeavesSurvivorsRunning(t *testing.T) {
	cmd, childPid, release := startHelperTree(t, true)
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	release()
	if waitGone(t, childPid, time.Second) {
		t.Fatalf("closing the released job killed grandchild %d", childPid)
	}
	if h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(childPid)); err == nil {
		_ = windows.TerminateProcess(h, 1)
		_ = windows.CloseHandle(h)
	}
}
