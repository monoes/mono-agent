package main

import "testing"

// TestRevealFolderCommand is a regression test for the HIGH finding at
// app.go:1045 — RevealProfileFolder unconditionally ran the macOS-only
// "open" command, which fails with "executable file not found in $PATH" on
// the Windows and Linux builds the release pipeline also ships.
// revealFolderCommand mirrors how updater.go's cliAssetNameFor makes GOOS
// injectable for testing.
func TestRevealFolderCommand(t *testing.T) {
	const dir = "/some/profile/dir"
	cases := []struct {
		goos     string
		wantName string
	}{
		{"darwin", "open"},
		{"windows", "explorer"},
		{"linux", "xdg-open"},
		// Other unix-likes fall back to xdg-open too.
		{"freebsd", "xdg-open"},
	}
	for _, tc := range cases {
		t.Run(tc.goos, func(t *testing.T) {
			name, args := revealFolderCommand(tc.goos, dir)
			if name != tc.wantName {
				t.Fatalf("revealFolderCommand(%q, ...) name = %q, want %q", tc.goos, name, tc.wantName)
			}
			if len(args) != 1 || args[0] != dir {
				t.Fatalf("revealFolderCommand(%q, ...) args = %v, want [%q]", tc.goos, args, dir)
			}
		})
	}
}
