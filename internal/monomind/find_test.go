package monomind

import (
	"runtime"
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
