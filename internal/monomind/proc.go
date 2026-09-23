package monomind

import "os/exec"

// KillProcessTree kills a started child and every process it spawned: its
// Job Object or, failing that, taskkill /T on Windows; SIGTERM then SIGKILL
// to its process group on unix, where cmd must have been started with
// Setpgid. For callers outside this package (the Wails GUI's chat) that
// start their own subprocesses. On Windows it is a no-op once cmd has been
// waited for, since the pid may have been reused by then.
func KillProcessTree(cmd *exec.Cmd) {
	killProcessGroup(cmd, 0)
}

// cloneForRetry copies what a detached start sets, for startDetached's
// retry on Windows (an exec.Cmd cannot be started twice). Its stdio is nil
// or an *os.File the caller owns, never a pipe exec made, so sharing it is
// safe.
func cloneForRetry(cmd *exec.Cmd) *exec.Cmd {
	c := &exec.Cmd{
		Path:   cmd.Path,
		Args:   cmd.Args,
		Env:    cmd.Env,
		Dir:    cmd.Dir,
		Stdin:  cmd.Stdin,
		Stdout: cmd.Stdout,
		Stderr: cmd.Stderr,
	}
	if cmd.SysProcAttr != nil {
		attr := *cmd.SysProcAttr
		c.SysProcAttr = &attr
	}
	return c
}
