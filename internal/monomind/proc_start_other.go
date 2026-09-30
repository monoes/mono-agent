//go:build !windows && !linux

package monomind

import "os/exec"

// startProcessGroup starts a child this process will kill with
// killProcessGroup. Setpgid already makes the group at fork, so it is a
// plain Start; the returned release is a no-op. There is no parent-death
// signal here (macOS has none): a child whose parent dies without
// cancelling is left to notice on its own (monoes/mono-agent#235).
func startProcessGroup(cmd *exec.Cmd) (release func(), err error) {
	return func() {}, cmd.Start()
}
