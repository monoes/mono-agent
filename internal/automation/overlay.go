package automation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/monoes/mono-agent/internal/action"
)

// The local overlay (<root>/<id>/overlay/selectors.json) carries healed
// selectors for packages whose files are never rewritten (built-in,
// imported, recorded). An overlay item never shadows a later package
// version blindly:
//
//   - a promotion stores the promoted candidate by content and applies only
//     while an equal candidate is still in the (package) entry;
//   - a full replacement entry stores the hash of the package entry it
//     replaced and applies only while that entry is unchanged.
//
// Stale items are ignored when read and pruned on every version change.

const overlayFormat = 2

type overlayEntry struct {
	Promote    *action.SelectorCandidate `json:"promote,omitempty"`
	Entry      *action.SelectorEntry     `json:"entry,omitempty"`
	BaseSHA256 string                    `json:"baseSha256,omitempty"` // package entry Entry replaced ("" = key was absent)
}

type overlayFile struct {
	Version   int                     `json:"version"`
	Selectors map[string]overlayEntry `json:"selectors"`
}

func (r *Registry) overlayPath(id string) string {
	return filepath.Join(r.root, id, "overlay", "selectors.json")
}

// loadOverlay reads the overlay. A pre-v2 file (key → entry) is read as
// "promote the entry's first candidate".
func (r *Registry) loadOverlay(id string) map[string]overlayEntry {
	out := map[string]overlayEntry{}
	b, err := os.ReadFile(r.overlayPath(id))
	if err != nil {
		return out
	}
	var f overlayFile
	if json.Unmarshal(b, &f) == nil && f.Version == overlayFormat {
		for k, e := range f.Selectors {
			out[k] = e
		}
		return out
	}
	var legacy map[string]action.SelectorEntry
	if json.Unmarshal(b, &legacy) == nil {
		for k, e := range legacy {
			if len(e.Candidates) > 0 {
				c := e.Candidates[0]
				out[k] = overlayEntry{Promote: &c}
			}
		}
	}
	return out
}

func (r *Registry) saveOverlayLocked(id string, ov map[string]overlayEntry) error {
	path := r.overlayPath(id)
	if len(ov) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(overlayFile{Version: overlayFormat, Selectors: ov}, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, append(b, '\n'))
}

// selectorHash identifies a package selector entry's content ("" = absent).
func selectorHash(e *action.SelectorEntry) string {
	if e == nil {
		return ""
	}
	b, _ := json.Marshal(e)
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// applyOverlay returns the effective entry for base (nil = key absent from
// the package) under ov, and whether each overlay part still applies.
func applyOverlay(base *action.SelectorEntry, ov overlayEntry) (eff *action.SelectorEntry, entryOK, promoteOK bool) {
	eff = base
	if ov.Entry != nil && ov.BaseSHA256 == selectorHash(base) {
		e := *ov.Entry
		eff, entryOK = &e, true
	}
	if ov.Promote != nil && eff != nil {
		if moved, changed := moveCandidateFirst(*eff, *ov.Promote); changed {
			eff = &moved
			promoteOK = true
		} else if len(eff.Candidates) > 0 && reflect.DeepEqual(eff.Candidates[0], *ov.Promote) {
			promoteOK = true // already first: harmless, keep
		}
	}
	return eff, entryOK, promoteOK
}

// pruneOverlayLocked drops overlay parts that no longer apply to the
// package in dir (after a version change). The caller holds the lock.
func (r *Registry) pruneOverlayLocked(id, dir string) error {
	ov := r.loadOverlay(id)
	if len(ov) == 0 {
		return nil
	}
	p, err := OpenDir(dir)
	if err != nil {
		return nil
	}
	sel, _ := p.Selectors()
	changed := false
	for k, o := range ov {
		var base *action.SelectorEntry
		if e, ok := sel[k]; ok {
			base = &e
		}
		_, entryOK, promoteOK := applyOverlay(base, o)
		n := o
		if !entryOK {
			n.Entry, n.BaseSHA256 = nil, ""
		}
		if !promoteOK {
			n.Promote = nil
		}
		switch {
		case n.Entry == nil && n.Promote == nil:
			delete(ov, k)
			changed = true
		case !reflect.DeepEqual(n, o):
			ov[k] = n
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return r.saveOverlayLocked(id, ov)
}

// readOverlay returns the effective overlaid entries of package id's
// current version (keys whose overlay still applies).
func (r *Registry) readOverlay(id string) map[string]action.SelectorEntry {
	out := map[string]action.SelectorEntry{}
	ov := r.loadOverlay(id)
	if len(ov) == 0 {
		return out
	}
	e, err := r.entry(id)
	if err != nil {
		return out
	}
	p, err := OpenDir(r.versionDir(id, e.Version))
	if err != nil {
		return out
	}
	sel, _ := p.Selectors()
	for k, o := range ov {
		var base *action.SelectorEntry
		if b, ok := sel[k]; ok {
			base = &b
		}
		if eff, entryOK, promoteOK := applyOverlay(base, o); (entryOK || promoteOK) && eff != nil {
			out[k] = *eff
		}
	}
	return out
}

func (r *Registry) currentSelector(idx *indexFile, id, key string) (*action.SelectorEntry, error) {
	e, ok := idx.Packages[id]
	if !ok || e.Removed {
		return nil, fmt.Errorf("%w: %s", ErrNotInstalled, id)
	}
	p, err := OpenDir(r.versionDir(id, e.Version))
	if err != nil {
		return nil, err
	}
	sel, err := p.Selectors()
	if err != nil {
		return nil, err
	}
	if b, ok := sel[key]; ok {
		return &b, nil
	}
	return nil, nil
}

// WriteOverlaySelector records a healed selector in the local overlay
// without touching package files. It applies while the package's own entry
// for key stays as it is now; a package update that changes it wins.
func (r *Registry) WriteOverlaySelector(id, key string, e action.SelectorEntry) error {
	if strings.TrimSpace(key) == "" {
		return errors.New("automation: empty selector key")
	}
	return r.update(func(idx *indexFile) (bool, error) {
		base, err := r.currentSelector(idx, id, key)
		if err != nil {
			return false, err
		}
		ov := r.loadOverlay(id)
		ov[key] = overlayEntry{Entry: &e, BaseSHA256: selectorHash(base)}
		return true, r.saveOverlayLocked(id, ov)
	})
}

// PromoteOverlayCandidate moves candidate c (matched by content) to the
// front of key's effective entry through the overlay. It is a no-op when c
// is already first or not a candidate. The promotion lapses when a later
// package version no longer has c.
func (r *Registry) PromoteOverlayCandidate(id, key string, c action.SelectorCandidate) error {
	return r.update(func(idx *indexFile) (bool, error) {
		base, err := r.currentSelector(idx, id, key)
		if err != nil {
			return false, err
		}
		ov := r.loadOverlay(id)
		o := ov[key]
		eff, _, _ := applyOverlay(base, o)
		if eff == nil {
			return false, nil
		}
		if _, changed := moveCandidateFirst(*eff, c); !changed {
			return false, nil
		}
		o.Promote = &c
		ov[key] = o
		return true, r.saveOverlayLocked(id, ov)
	})
}
