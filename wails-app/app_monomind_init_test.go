package main

import (
	"os"
	"path/filepath"
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
