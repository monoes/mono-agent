package main

import (
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
)

func TestIsMonomindInitializedAt(t *testing.T) {
	root := t.TempDir()

	if isMonomindInitializedAt(root) {
		t.Fatal("expected false for a profile folder with no .monomind/config.yaml")
	}

	monomindDir := filepath.Join(root, ".monomind")
	if err := os.MkdirAll(monomindDir, 0700); err != nil {
		t.Fatal(err)
	}
	if isMonomindInitializedAt(root) {
		t.Fatal("expected false: .monomind/ exists but config.yaml does not (EnsureLayout creates the bare dir on every org call)")
	}

	if err := os.WriteFile(filepath.Join(monomindDir, "config.yaml"), []byte("name: test\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if !isMonomindInitializedAt(root) {
		t.Fatal("expected true once .monomind/config.yaml exists")
	}
}

// TestInitFailureMessage_127KeepsOutputAndHints guards the reported
// "Failed: initialize monomind / exit status 127" with nothing else to go on.
func TestInitFailureMessage_127KeepsOutputAndHints(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("needs sh")
	}
	err := exec.Command("sh", "-c", "exit 127").Run()
	msg := initFailureMessage("/x/bin/monomind", err, []string{"env: node: No such file or directory"})
	for _, want := range []string{"exit status 127", "/x/bin/monomind", "node", "env: node: No such file or directory"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q missing %q", msg, want)
		}
	}
}
