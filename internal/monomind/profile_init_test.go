package monomind

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestIsInitializedAt(t *testing.T) {
	root := t.TempDir()
	if IsInitializedAt(root) {
		t.Fatal("expected false for a folder with no .monomind/config.yaml")
	}
	dir := filepath.Join(root, ".monomind")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if IsInitializedAt(root) {
		t.Fatal("expected false: .monomind/ exists but config.yaml does not (EnsureLayout creates the bare dir)")
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("name: test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !IsInitializedAt(root) {
		t.Fatal("expected true once .monomind/config.yaml exists")
	}
}

// TestInitFailureMessage_127KeepsOutputAndHints guards the reported
// "Failed: initialize monomind / exit status 127" with nothing else to go on.
func TestInitFailureMessage_127KeepsOutputAndHints(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs sh")
	}
	err := exec.Command("sh", "-c", "exit 127").Run()
	msg := InitFailureMessage("/x/bin/monomind", err, []string{"env: node: No such file or directory"})
	for _, want := range []string{"exit status 127", "/x/bin/monomind", "node", "env: node: No such file or directory"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q missing %q", msg, want)
		}
	}
}
