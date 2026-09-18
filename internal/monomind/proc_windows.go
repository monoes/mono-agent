//go:build windows

package monomind

import (
	"os"
	"os/exec"
)

// setProcessGroup is a no-op on Windows in this build: full Job Object
// support is the dedicated Windows pass (plan §9.6 — chat/orgs are
// best-effort there until that lands).
func setProcessGroup(cmd *exec.Cmd) {}

// killProcessGroup falls back to killing the direct child; the agent-CLI
// grandchild is left to monomind's own SIGTERM ladder.
func killProcessGroup(cmd *exec.Cmd, _ int) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

// signalServe kills a serve daemon by pid: Windows has no SIGTERM, so both
// steps are a kill, and its agent-CLI grandchildren are not reached (plan
// §15 "Windows: provider process groups are not killed").
func signalServe(pid int, _ bool) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return nil // already gone
	}
	return p.Kill()
}
