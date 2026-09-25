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
//   - local packages (the user's own): selectors.json in the installed
//     version is rewritten in place and the index's installedSha256 is
//     refreshed, so the registry never mistakes the change for a foreign
//     edit;
//   - everything else: the registry's overlay records "promote c" for the
//     key (PromoteOverlayCandidate), so package files — and updates — are
//     never touched; the promotion lapses when a new version drops c.
//
// The candidate is identified by content, never by index, and each
// read-modify-write runs under the registry's update lock: promoting a
// candidate that is already first (or no longer present) changes nothing.

// PromoteCandidate moves candidate c of selector key to the front of
// package id's effective entry. Only a package that is the user's own
// (source local, trust local or recorded) is rewritten in place;
// everything else — built-in, imported, or a built-in carrying local
// trust — is promoted through the overlay (PromoteOverlayCandidate).
func (r *Registry) PromoteCandidate(id, key string, c action.SelectorCandidate) error {
	errNotLocal := errors.New("not a local package")
	err := r.update(func(idx *indexFile) (bool, error) {
		e, ok := idx.Packages[id]
		if !ok || e.Removed {
			return false, fmt.Errorf("%w: %s", ErrNotInstalled, id)
		}
		if !e.userOwned() { // source local, trust local or recorded (same rule as ReplaceSelector)
			return false, errNotLocal
		}
		dir := r.versionDir(id, e.Version)
		cur, found, err := readPackageSelector(dir, key)
		if err != nil || !found {
			return false, err
		}
		promoted, changed := moveCandidateFirst(cur, c)
		if !changed {
			return false, nil
		}
		if err := writePackageSelector(dir, key, promoted); err != nil {
			return false, err
		}
		h, err := fsHash(os.DirFS(dir))
		if err != nil {
			return false, err
		}
		e.InstalledSha256 = h
		return true, nil // also bumps the registry generation (cache invalidation)
	})
	if errors.Is(err, errNotLocal) {
		return r.PromoteOverlayCandidate(id, key, c)
	}
	return err
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

// readPackageSelector reads selectors.json[key] from a package directory.
func readPackageSelector(dir, key string) (action.SelectorEntry, bool, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "selectors.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return action.SelectorEntry{}, false, nil
	}
	if err != nil {
		return action.SelectorEntry{}, false, err
	}
	all := map[string]action.SelectorEntry{}
	if err := json.Unmarshal(raw, &all); err != nil {
		return action.SelectorEntry{}, false, fmt.Errorf("parse %s/selectors.json: %w", dir, err)
	}
	e, ok := all[key]
	return e, ok, nil
}

// writePackageSelector replaces selectors.json[key] in a package directory,
// keeping every other entry byte-for-byte.
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
	return atomicWrite(path, append(out, '\n'))
}
