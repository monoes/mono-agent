package monomind

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestExecStderrOption: ExecOptions.Stderr receives the process's stderr.
func TestExecStderrOption(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake monomind is a shell script")
	}
	bin := filepath.Join(t.TempDir(), "fake-stderr.sh")
	script := "#!/bin/sh\necho 'diag: no credentials' >&2\nexit 3\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	res, _ := Exec(context.Background(), ExecOptions{Bin: bin, Runtime: "claude", Prompt: "hi", Stderr: &buf}, nil)
	if !strings.Contains(buf.String(), "diag: no credentials") {
		t.Errorf("stderr = %q (res %+v)", buf.String(), res)
	}
}
