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
//   - built-in and imported packages: the reordered entry goes to the local
//     overlay, so package files — and updates — are never touched, and the
//     overlay can be exported as a patch.
//
// The candidate is identified by content, never by index, and the whole
// read-modify-write runs under the registry's update lock: promoting a
// candidate that is already first (or no longer present) changes nothing.

// PromoteCandidate moves candidate c of selector key to the front of the
// effective entry (overlay wins) of package id.
func (r *Registry) PromoteCandidate(id, key string, c action.SelectorCandidate) error {
	return r.update(func(idx *indexFile) (bool, error) {
		e, ok := idx.Packages[id]
		if !ok || e.Removed {
			return false, fmt.Errorf("%w: %s", ErrNotInstalled, id)
		}
		dir := r.versionDir(id, e.Version)
		ov := r.readOverlay(id)
		if cur, inOverlay := ov[key]; inOverlay || e.Source != SourceLocal {
			if !inOverlay {
				base, found, err := readPackageSelector(dir, key)
				if err != nil || !found {
					return false, err
				}
				cur = base
			}
			promoted, changed := moveCandidateFirst(cur, c)
			if !changed {
				return false, nil
			}
			return false, r.writeOverlayLocked(id, ov, key, promoted)
		}
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
		return true, nil
	})
}

// writeOverlayLocked writes the overlay with key set to e. The caller holds
// the update lock.
func (r *Registry) writeOverlayLocked(id string, ov map[string]action.SelectorEntry, key string, e action.SelectorEntry) error {
	dir := filepath.Join(r.root, id, "overlay")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	ov[key] = e
	b, err := json.MarshalIndent(ov, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(dir, "selectors.json"), append(b, '\n'))
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
