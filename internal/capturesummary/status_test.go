package capturesummary

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStateOf(t *testing.T) {
	now := time.Now()
	dir := t.TempDir()
	if got := StateOf(dir, now); got != "" {
		t.Errorf("no summary asked for: %q", got)
	}
	if got := StateOf("", now); got != "" {
		t.Errorf("not a capture: %q", got)
	}
	if err := os.WriteFile(filepath.Join(dir, SummaryFile), []byte("# s"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := StateOf(dir, now); got != StateDone {
		t.Errorf("summary.md alone: %q", got)
	}
	if err := writeStatus(dir, Status{Status: StatePending, RequestedAt: stamp(now.Add(-2 * StaleAfter))}); err != nil {
		t.Fatal(err)
	}
	if got := StateOf(dir, now); got != StateStalled {
		t.Errorf("abandoned pending: %q", got)
	}
	if err := writeStatus(dir, Status{Status: StateError, Error: "x"}); err != nil {
		t.Fatal(err)
	}
	if got := StateOf(dir, now); got != StateError {
		t.Errorf("error: %q", got)
	}
	// The atomic write leaves no temp file behind for a listing to find.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != SummaryFile && e.Name() != StatusFile {
			t.Errorf("stray file %s", e.Name())
		}
	}
}
