package recording

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/monoes/mono-agent/internal/capture"
)

// MaxRecordingsPerInbox bounds how many recordings one inbox keeps
// (security review L3). Past it the oldest are removed when a new one
// lands — recordings not yet saved into an automation first.
const MaxRecordingsPerInbox = 200

// pruneInbox removes the oldest recordings in inbox beyond max, never the
// one at keep, and returns a warning per recording removed.
func pruneInbox(inbox, keep string, max int) []string {
	entries, err := capture.List(inbox)
	if err != nil {
		return nil
	}
	var recs []Summary
	for _, e := range entries {
		if e.Meta.Source == SourceRecording {
			recs = append(recs, summaryOf(e.Path, &e.Meta))
		}
	}
	excess := len(recs) - max
	if excess <= 0 {
		return nil
	}
	// Removal order: unlinked before linked (a linked one is an
	// automation's provenance), then oldest first.
	sort.SliceStable(recs, func(i, j int) bool {
		li, lj := recs[i].Automation != "", recs[j].Automation != ""
		if li != lj {
			return !li
		}
		return recs[i].StartedAt < recs[j].StartedAt
	})
	var warnings []string
	for _, r := range recs {
		if excess == 0 {
			break
		}
		if filepath.Clean(r.Dir) == filepath.Clean(keep) {
			continue
		}
		if err := os.RemoveAll(r.Dir); err != nil {
			continue
		}
		excess--
		warnings = append(warnings, fmt.Sprintf("removed old recording %s: the inbox keeps at most %d", r.ID, max))
	}
	return warnings
}
