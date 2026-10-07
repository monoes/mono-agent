package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
	"github.com/monoes/mono-agent/internal/monomind"
)

// The binary installs account.Require as monomind's gate (monomind_gate.go), so
// a locked account stops an agent turn before monomind is even looked for. In
// this test binary Exec would otherwise run: there is no gate and no guard.
func TestExecIsGatedByTheAccountInTheCLI(t *testing.T) {
	accounttest.Install(t, accounttest.LockedNoLogin)

	_, err := monomind.Exec(context.Background(),
		monomind.ExecOptions{Bin: filepath.Join(t.TempDir(), "never-run"), Runtime: "codex", Prompt: "hi"}, nil)

	if !account.IsLoginRequired(err) {
		t.Fatalf("err = %v, want *account.LoginRequiredError", err)
	}
}
