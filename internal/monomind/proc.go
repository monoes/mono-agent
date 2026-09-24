package monomind

import "os/exec"

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

// StartDetached starts cmd in its own process group / detached from this
// console so it outlives the caller (e.g. `doctor` starting the daemon).
func StartDetached(cmd *exec.Cmd) (*exec.Cmd, error) { return startDetached(cmd) }
