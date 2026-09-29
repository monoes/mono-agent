//go:build linux

package monomind

import (
	"os/exec"
	"runtime"
	"sync"
	"syscall"
)

// startProcessGroup starts a child this process will kill with
// killProcessGroup. Setpgid already makes the group at fork. The child also
// gets SIGTERM as its parent-death signal, so it ends when this process dies
// without cancelling it (SIGKILL, a crash, SIGPIPE on stdout) instead of
// being orphaned, as KILL_ON_JOB_CLOSE does on Windows (monoes/mono-agent#235).
//
// Linux sends that signal when the thread that forked the child exits, not
// the process, and the Go runtime ends a thread whose goroutine exits while
// locked to it. So the child is started from a goroutine that locks its
// thread and holds it until release, which callers call once the child has
// been waited for: no other goroutine can run on that thread, and so none
// can end it, while the child lives.
func startProcessGroup(cmd *exec.Cmd) (release func(), err error) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Pdeathsig = syscall.SIGTERM

	started := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		err := cmd.Start()
		started <- err
		if err == nil {
			<-done
		}
	}()
	if err := <-started; err != nil {
		return func() {}, err
	}
	var once sync.Once
	return func() { once.Do(func() { close(done) }) }, nil
}
