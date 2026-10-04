package monomind

import (
	"os"
	"path/filepath"
	"testing"
)

// A future Freebuff runner must receive the same executable pinning as
// other runtimes, including failure closed when a shim remains on PATH.
func TestFreebuffExecutablePin(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "freebuff")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	for _, tc := range []struct {
		name, override, searchPath, want string
		shims                            bool
	}{
		{"discovered", "", dir, bin, false},
		{"absolute override", bin, t.TempDir(), bin, false},
		{"relative override", "freebuff", dir, bin, false},
		{"missing", filepath.Join(dir, "missing"), dir, "", false},
		{"missing with shims", filepath.Join(dir, "missing"), dir, unpinnedPath("freebuff"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetRuntimePins()
			t.Cleanup(resetRuntimePins)
			env := pinRuntimes([]string{"FREEBUFF_CLI_BIN=" + tc.override, "KEEP=value"}, tc.searchPath, tc.shims)
			got, present := envValue(env, "FREEBUFF_CLI_BIN")
			if got != tc.want || present != (tc.want != "") {
				t.Fatalf("pin = %q (present %v), want %q", got, present, tc.want)
			}
			if got, _ := envValue(env, "KEEP"); got != "value" {
				t.Fatalf("lost unrelated environment: %v", env)
			}
		})
	}
}
