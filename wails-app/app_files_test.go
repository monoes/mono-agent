package main

import (
	"path/filepath"
	"testing"
)

// TestOpenFileCommand mirrors TestRevealFolderCommand's pattern for
// revealFolderCommand: verifies the per-GOOS command selection used to hand
// a file off to the OS's default application, without actually launching one.
func TestOpenFileCommand(t *testing.T) {
	const path = "/some/vault/documents/doc-001.pdf"
	cases := []struct {
		goos     string
		wantName string
		wantArgs []string
	}{
		{"darwin", "open", []string{path}},
		{"windows", "cmd", []string{"/c", "start", "", path}},
		{"linux", "xdg-open", []string{path}},
		// Other unix-likes fall back to xdg-open too.
		{"freebsd", "xdg-open", []string{path}},
	}
	for _, tc := range cases {
		t.Run(tc.goos, func(t *testing.T) {
			name, args := openFileCommand(tc.goos, path)
			if name != tc.wantName {
				t.Fatalf("openFileCommand(%q, ...) name = %q, want %q", tc.goos, name, tc.wantName)
			}
			if len(args) != len(tc.wantArgs) {
				t.Fatalf("openFileCommand(%q, ...) args = %v, want %v", tc.goos, args, tc.wantArgs)
			}
			for i := range args {
				if args[i] != tc.wantArgs[i] {
					t.Fatalf("openFileCommand(%q, ...) args = %v, want %v", tc.goos, args, tc.wantArgs)
				}
			}
		})
	}
}

func TestOpenPathWithOSRejectsMissingFile(t *testing.T) {
	a := &App{}
	err := a.OpenPathWithOS(filepath.Join(t.TempDir(), "does-not-exist.pdf"))
	if err == nil {
		t.Fatal("expected an error for a nonexistent path, got nil")
	}
}
