package automation

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

// FoldLegacy folds new or changed ~/.monoagent/actions/<p> directories
// into local-<p> packages, as seeding used to on every start. The app no
// longer ships built-in packages to seed (official ones come from
// monoes.me), so a legacy platform's site and policy are taken from the
// installed package that owns it, when there is one. Nothing is written
// when no legacy directory changed.
func (r *Registry) FoldLegacy() (*SeedReport, error) {
	rep := &SeedReport{}
	idx, err := r.readIndex()
	if err != nil {
		return rep, err
	}
	legacy := scanLegacy(filepath.Join(r.home, "actions"))
	if !legacyChanged(idx, legacy) {
		return rep, nil
	}
	err = r.update(func(idx *indexFile) (bool, error) {
		if !legacyChanged(idx, legacy) {
			return false, nil
		}
		wrapped, err := r.foldLegacyLocked(idx, r.installedSeedsLocked(idx), legacy)
		if err != nil {
			return false, err
		}
		rep.LegacyWrapped = wrapped
		return true, nil
	})
	return rep, err
}

// installedSeedsLocked returns the installed, non-legacy packages in the
// shape the legacy fold reads platform metadata from (sorted by id so the
// fold is deterministic).
func (r *Registry) installedSeedsLocked(idx *indexFile) []seedPkg {
	ids := make([]string, 0, len(idx.Packages))
	for id, e := range idx.Packages {
		if !e.Removed && !strings.HasPrefix(id, "local-") {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	var out []seedPkg
	for _, id := range ids {
		p, err := OpenDir(r.versionDir(id, idx.Packages[id].Version))
		if err != nil || p.Manifest.Legacy != nil {
			continue
		}
		out = append(out, seedPkg{pkg: p})
	}
	return out
}

// TestSeed, when set, is seeded by Boot like the built-ins used to be. Only
// tests set it (to the repo's automations.FS()), so code paths that expect
// the official packages installed can run against a throwaway home; the app
// never sets it.
var TestSeed fs.FS

// Boot prepares the registry for use at startup: it folds legacy action
// directories (FoldLegacy), or under tests seeds TestSeed (which folds them
// too).
func (r *Registry) Boot() (*SeedReport, error) {
	if TestSeed != nil {
		return r.SeedWithReport(TestSeed)
	}
	return r.FoldLegacy()
}
