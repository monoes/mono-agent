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
	"unsafe"

	"golang.org/x/sys/windows"
)

// procHelperEnv makes the test binary act as a helper process: "parent"
// starts a "child" copy of itself, prints the child's pid and sleeps;
// "child" just sleeps.
const procHelperEnv = "MONOMIND_PROC_TEST_HELPER"

func TestProcHelperProcess(t *testing.T) {
	switch os.Getenv(procHelperEnv) {
	case "parent", "detach-parent":
		// The child is spawned at once, before anything else runs: the
		// window in which it used to escape a job assigned after Start.
		child := exec.Command(os.Args[0], "-test.run=^TestProcHelperProcess$")
		child.Env = append(os.Environ(), procHelperEnv+"=child")
		var err error
		if os.Getenv(procHelperEnv) == "detach-parent" {
			child, err = startDetached(child)
		} else {
			err = child.Start()
		}
		if err != nil {
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
	return startHelperTreeMode(t, "parent", attach)
}

func startHelperTreeMode(t *testing.T, mode string, attach bool) (*exec.Cmd, int, func()) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestProcHelperProcess$")
	cmd.Env = append(os.Environ(), procHelperEnv+"="+mode)
	setProcessGroup(cmd)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	release := func() {}
	if attach {
		release, err = startProcessGroup(cmd)
	} else {
		err = cmd.Start()
	}
	if err != nil {
		t.Fatal(err)
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
	terminatePid(childPid)
}

func terminatePid(pid int) {
	if h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid)); err == nil {
		_ = windows.TerminateProcess(h, 1)
		_ = windows.CloseHandle(h)
	}
}

var procIsProcessInJob = windows.NewLazySystemDLL("kernel32.dll").NewProc("IsProcessInJob")

// inJob reports whether pid is a member of job.
func inJob(t *testing.T, pid int, job windows.Handle) bool {
	t.Helper()
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		t.Fatalf("open pid %d: %v", pid, err)
	}
	defer windows.CloseHandle(h)
	var result int32
	r, _, callErr := procIsProcessInJob.Call(uintptr(h), uintptr(job), uintptr(unsafe.Pointer(&result)))
	if r == 0 {
		t.Fatalf("IsProcessInJob: %v", callErr)
	}
	return result != 0
}

// The helper spawns its child as its very first act, so before the suspend
// → assign → resume start the child regularly landed outside the job.
func TestStartProcessGroupGrandchildIsInJob(t *testing.T) {
	cmd, childPid, release := startHelperTree(t, true)
	defer func() {
		killProcessGroup(cmd, cmd.Process.Pid)
		_ = cmd.Wait()
		release()
	}()
	v, ok := jobs.Load(cmd)
	if !ok {
		t.Fatal("startProcessGroup did not record a job")
	}
	if !inJob(t, childPid, v.(*jobGroup).job) {
		t.Fatalf("grandchild %d started outside the job", childPid)
	}
	if cmd.SysProcAttr.CreationFlags&windows.CREATE_SUSPENDED != 0 {
		t.Fatal("CREATE_SUSPENDED left set on the command")
	}
}

// A detached start from inside one of our jobs breaks away, so terminating
// that job (a killed chat turn) leaves the detached org run or serve alive.
func TestStartDetachedBreaksAwayFromJob(t *testing.T) {
	cmd, childPid, release := startHelperTreeMode(t, "detach-parent", true)
	defer release()
	defer terminatePid(childPid)
	killProcessGroup(cmd, 0)
	_ = cmd.Wait()
	if waitGone(t, childPid, time.Second) {
		t.Fatalf("detached child %d died with the job it broke away from", childPid)
	}
}
