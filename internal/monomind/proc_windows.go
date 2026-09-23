//go:build windows

package monomind

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows has no process groups a signal can target. A child this process
// will kill (Exec, OrgRun, OrgEvents, OrgServeRun) is put in a Job Object
// by startProcessGroup (proc_windows_start.go) before its first instruction
// runs; killProcessGroup terminates the job, which takes
// every descendant with it. The job also has KILL_ON_JOB_CLOSE, so if this
// process dies without cleaning up, Windows closes the handle and kills the
// tree instead of orphaning it. On a normal exit release lifts that limit
// before closing the handle, so descendants that outlive the child behave as
// on unix. BREAKAWAY_OK lets a descendant that asks to leave the job
// (CREATE_BREAKAWAY_FROM_JOB, e.g. a deliberately detached daemon) do so, as
// setsid escapes a process group on unix; startDetached does exactly that
// for the children that must outlive this process.
//
// killProcessGroup's taskkill fallback covers the case where making or
// assigning the job failed altogether.

// setProcessGroup starts the child in a new console process group, so a
// Ctrl+C in this console does not reach it, as with Setpgid on unix.
func setProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NEW_PROCESS_GROUP
}

const (
	jobLimitsAttached = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_BREAKAWAY_OK
	jobLimitsReleased = windows.JOB_OBJECT_LIMIT_BREAKAWAY_OK
)

// jobGroup is the Job Object holding one started child and its descendants.
type jobGroup struct {
	mu  sync.Mutex
	job windows.Handle // 0 once terminated or released
}

// jobs maps a started *exec.Cmd to its *jobGroup.
var jobs sync.Map

// attachProcessGroup puts the started (still suspended) child in a new Job
// Object. If that fails the child still runs, and killProcessGroup falls
// back to taskkill. Call release once the child has been waited for.
func attachProcessGroup(cmd *exec.Cmd) (release func()) {
	if cmd.Process == nil {
		return func() {}
	}
	job, err := newJob()
	if err != nil {
		return func() {}
	}
	if err := assignToJob(job, cmd.Process.Pid); err != nil {
		_ = windows.CloseHandle(job)
		return func() {}
	}
	g := &jobGroup{job: job}
	jobs.Store(cmd, g)
	return func() {
		jobs.Delete(cmd)
		g.release()
	}
}

func newJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	if err := setJobLimits(job, jobLimitsAttached); err != nil {
		_ = windows.CloseHandle(job)
		return 0, err
	}
	return job, nil
}

func setJobLimits(job windows.Handle, flags uint32) error {
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = flags
	_, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
	return err
}

func assignToJob(job windows.Handle, pid int) error {
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	return windows.AssignProcessToJobObject(job, h)
}

// terminate kills every process in the job. It reports false when the job is
// already gone or termination failed, so the caller can fall back.
func (g *jobGroup) terminate() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.job == 0 {
		return false
	}
	err := windows.TerminateJobObject(g.job, 1)
	_ = windows.CloseHandle(g.job)
	g.job = 0
	return err == nil
}

// release closes the job without killing what is left in it.
func (g *jobGroup) release() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.job == 0 {
		return
	}
	if err := setJobLimits(g.job, jobLimitsReleased); err != nil {
		// Closing now would kill surviving descendants; keep the handle
		// (and so the job) for the life of this process instead.
		g.job = 0
		return
	}
	_ = windows.CloseHandle(g.job)
	g.job = 0
}

// killProcessGroup kills the child and its descendants: through its Job
// Object when attachProcessGroup made one, otherwise with taskkill /T, which
// follows parent pids (so it misses descendants whose parent already exited).
func killProcessGroup(cmd *exec.Cmd, pid int) {
	if v, ok := jobs.Load(cmd); ok && v.(*jobGroup).terminate() {
		return
	}
	if cmd.Process != nil {
		// Once Wait has reaped the child its handle is closed and the pid
		// may already name an unrelated process: taskkill must not touch it.
		if err := cmd.Process.Signal(syscall.Signal(0)); errors.Is(err, os.ErrProcessDone) {
			return
		}
		if pid == 0 {
			pid = cmd.Process.Pid
		}
	}
	if pid != 0 {
		taskkillTree(pid)
	}
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

// taskkillTree runs taskkill /T /F from System32, never from PATH.
func taskkillTree(pid int) {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	tk := exec.Command(filepath.Join(root, "System32", "taskkill.exe"), "/T", "/F", "/PID", strconv.Itoa(pid))
	tk.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	_ = tk.Run()
}

// signalServe kills a serve daemon and its agent-CLI descendants by pid.
// Windows has no SIGTERM, so both steps are a tree kill: taskkill /T follows
// parent pids, then the daemon itself is killed if taskkill missed it. The
// daemon is alive when this runs (OrgServeStop only signals a live
// heartbeat), so its children are found through it; the one gap is a
// descendant whose own parent already exited, which taskkill cannot link
// back. OrgServeStart deliberately gives the daemon no job to close that
// gap: a job this process holds would kill the daemon when we exit.
func signalServe(pid int, _ bool) error {
	taskkillTree(pid)
	p, err := os.FindProcess(pid)
	if err != nil {
		return nil // already gone
	}
	if err := p.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}
