package monomind

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// #261: a caller that needs its sandbox gets ErrSandboxRequired, and no
// turn, when the run-time decision can't apply it; before, the turn ran
// with no sandbox flag at all.
func TestExecRequireSandboxFailsClosed(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "monomind")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := Exec(context.Background(), ExecOptions{
		Bin: bin, Runtime: "copilot", Prompt: "investigate", Cwd: t.TempDir(), Access: AccessFull,
		Sandbox: SandboxReadOnly, RequireSandbox: true,
	}, nil)
	if !errors.Is(err, ErrSandboxRequired) {
		t.Fatalf("err = %v, want ErrSandboxRequired", err)
	}
}
