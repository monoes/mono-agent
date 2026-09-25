package automation

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/monoes/mono-agent/internal/action"
)

// Where ReplaceSelector wrote the entry.
const (
	WherePackage = "package"
	WhereOverlay = "overlay"
)

// ReplaceSelector replaces selector key of package id with e (a re-recorded
// selector, contracts §9). The key must already exist in the package's
// effective selectors. A package that is the user's own (source local,
// trust local or recorded) has its selectors.json rewritten in place under the lock: the version is kept,
// the installed hash refreshed and any overlay item for the key dropped.
// Every other package gets a full overlay entry, guarded by the hash of the
// package entry it replaces. where is WherePackage or WhereOverlay.
func (r *Registry) ReplaceSelector(id, key string, e action.SelectorEntry) (string, error) {
	if strings.TrimSpace(key) == "" {
		return "", errors.New("automation: empty selector key")
	}
	if err := checkSelectorEntry(key, e); err != nil {
		return "", err
	}
	var where string
	err := r.update(func(idx *indexFile) (bool, error) {
		ent, ok := idx.Packages[id]
		if !ok || ent.Removed {
			return false, fmt.Errorf("%w: %s", ErrNotInstalled, id)
		}
		base, err := r.currentSelector(idx, id, key)
		if err != nil {
			return false, err
		}
		ov := r.loadOverlay(id)
		if eff, _, _ := applyOverlay(base, ov[key]); eff == nil {
			return false, fmt.Errorf("automation %s has no selector %q", id, key)
		}

		if ent.userOwned() {
			dir := r.versionDir(id, ent.Version)
			if err := writePackageSelector(dir, key, e); err != nil {
				return false, err
			}
			h, err := fsHash(os.DirFS(dir))
			if err != nil {
				return false, err
			}
			ent.InstalledSha256 = h
			if _, had := ov[key]; had {
				delete(ov, key)
				if err := r.saveOverlayLocked(id, ov); err != nil {
					return false, err
				}
			}
			where = WherePackage
			return true, nil
		}

		// The re-recorded entry is authoritative: no earlier promotion
		// reorders it.
		ov[key] = overlayEntry{Entry: &e, BaseSHA256: selectorHash(base)}
		if err := r.saveOverlayLocked(id, ov); err != nil {
			return false, err
		}
		where = WhereOverlay
		return true, nil
	})
	if err != nil {
		return "", err
	}
	return where, nil
}

// checkSelectorEntry applies the selectors.json rules to one entry.
func checkSelectorEntry(key string, e action.SelectorEntry) error {
	if len(e.Candidates) == 0 {
		return fmt.Errorf("selector %q has no candidates", key)
	}
	for i, c := range e.Candidates {
		n := 0
		for _, set := range []bool{c.CSS != "", c.XPath != "", c.Aria != nil, c.Text != ""} {
			if set {
				n++
			}
		}
		if n != 1 {
			return fmt.Errorf("selector %q candidate %d must set exactly one of css, xpath, aria, text", key, i)
		}
	}
	return nil
}
