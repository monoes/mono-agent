package monomind

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestCandidatePaths_IncludesWellKnownHomebrewLocations guards against a
// real, reported gap: `npm install -g @monoes/monomindcli` — the exact
// command this app's own error message tells users to run — installs to
// /opt/homebrew/bin (Apple Silicon) or /usr/local/bin (Intel Mac, and
// commonly Linux) whenever npm's global prefix is Homebrew-managed, which
// is the default on a `brew install node` setup. exec.LookPath("monomind")
// alone isn't enough to find it: a GUI app launched via Finder/Dock/open
// does not inherit the interactive shell's PATH (Homebrew's shellenv PATH
// additions only apply inside a login/interactive shell), so a binary that
// is genuinely installed and on the *terminal's* PATH can still be
// invisible to the running app unless the discovery ladder also checks
// these well-known install roots directly, the same way it already does
// for ~/.npm-global/bin and ~/.local/bin.
func TestCandidatePaths_IncludesWellKnownHomebrewLocations(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Homebrew/Linux install roots are not applicable on Windows")
	}
	// Isolate from this test's own ambient PATH — the whole point is to
	// prove these are present as literal fallback candidates, not merely
	// resolved via exec.LookPath("monomind") because the environment
	// running the test happens to have Homebrew's bin dir on PATH (as a
	// developer's shell does, but a GUI-launched app does not).
	t.Setenv("PATH", t.TempDir())
	t.Setenv(EnvOverride, "")
	cands := CandidatePaths()
	for _, want := range []string{"/opt/homebrew/bin/monomind", "/usr/local/bin/monomind"} {
		found := false
		for _, c := range cands {
			if c == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("CandidatePaths() = %v, missing well-known install root %q", cands, want)
		}
	}
}

// TestFind_NvmInstallInvisibleToGUIPath reproduces the reported Mac mini
// failure: monomind installed under nvm, app launched from Finder with a
// PATH that has neither nvm's bin dir nor node. Find must locate the newest
// nvm install and put its dir on PATH so the `env node` shebang resolves.
func TestFind_NvmInstallInvisibleToGUIPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("nvm layout is unix-only")
	}
	home := t.TempDir()
	root := filepath.Join(home, ".nvm", "versions", "node")
	for _, v := range []string{"v9.11.2", "v22.12.0", "v18.20.4"} {
		bin := filepath.Join(root, v, "bin")
		if err := os.MkdirAll(bin, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bin, "monomind"), []byte("#!/usr/bin/env node\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv(EnvOverride, "")
	t.Setenv("PATH", "/usr/bin:/bin")

	got, err := Find()
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	wantDir := filepath.Join(root, "v22.12.0", "bin")
	if got != filepath.Join(wantDir, "monomind") {
		t.Fatalf("Find = %q, want newest nvm install under %q", got, wantDir)
	}
	if !strings.HasPrefix(os.Getenv("PATH"), wantDir+string(os.PathListSeparator)) {
		t.Fatalf("PATH = %q, want %q prepended so `node` resolves", os.Getenv("PATH"), wantDir)
	}
}
