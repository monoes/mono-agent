package automation

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"

	"github.com/monoes/mono-agent/internal/action"
)

// Healing promotion (spec §8.7): when a run found an element through a
// non-first candidate, that candidate moves to the front so the next run
// tries it first.
//
//   - local packages (the user's own): selectors.json in the package is
//     rewritten in place;
//   - built-in and imported packages: the reordered entry goes to the local
//     overlay (WriteOverlaySelector), so package files — and updates — are
//     never touched, and the overlay can be exported as a patch.
//
// The move is by candidate content, so promoting a candidate that is
// already first changes nothing (idempotent).

// PromoteSelector promotes candidate candidateIndex of selector key in the
// effective entry (overlay wins) of package id. Indexes ≤ 0 or out of range
// and unknown keys are no-ops.
func (r *Registry) PromoteSelector(id, key string, candidateIndex int) error {
	if candidateIndex <= 0 {
		return nil
	}
	pkg, err := r.Get(id)
	if err != nil {
		return err
	}
	ctx := pkg.Context()
	if ctx == nil {
		return nil
	}
	entry, ok := ctx.Selector(key)
	if !ok || entry == nil || candidateIndex >= len(entry.Candidates) {
		return nil
	}
	return r.promoteCandidate(pkg, key, *entry, entry.Candidates[candidateIndex])
}

func (r *Registry) promoteCandidate(pkg *Package, key string, entry action.SelectorEntry, c action.SelectorCandidate) error {
	promoted, changed := moveCandidateFirst(entry, c)
	if !changed {
		return nil
	}
	if pkg.Source == SourceLocal && pkg.Dir != "" {
		return writePackageSelector(pkg.Dir, key, promoted)
	}
	return r.WriteOverlaySelector(pkg.Manifest.ID, key, promoted)
}

// moveCandidateFirst returns entry with c moved to index 0. changed is
// false when c is already first or not present.
func moveCandidateFirst(entry action.SelectorEntry, c action.SelectorCandidate) (action.SelectorEntry, bool) {
	at := -1
	for i, cand := range entry.Candidates {
		if reflect.DeepEqual(cand, c) {
			at = i
			break
		}
	}
	if at <= 0 {
		return entry, false
	}
	out := entry
	out.Candidates = make([]action.SelectorCandidate, 0, len(entry.Candidates))
	out.Candidates = append(out.Candidates, entry.Candidates[at])
	out.Candidates = append(out.Candidates, entry.Candidates[:at]...)
	out.Candidates = append(out.Candidates, entry.Candidates[at+1:]...)
	return out, true
}

// writePackageSelector replaces selectors.json[key] in a package directory,
// keeping every other entry byte-for-byte, via temp file + rename.
func writePackageSelector(dir, key string, e action.SelectorEntry) error {
	path := filepath.Join(dir, "selectors.json")
	all := map[string]json.RawMessage{}
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(raw, &all); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
	case !errors.Is(err, fs.ErrNotExist):
		return err
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	all[key] = b
	out, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".selectors-*.json")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(append(out, '\n')); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}
