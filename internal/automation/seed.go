package automation

import (
	"fmt"
	"io/fs"
	"path/filepath"
)

// SeedReport says what a Seed call changed (ids per outcome).
type SeedReport struct {
	Installed     []string `json:"installed,omitempty"`     // first install
	Updated       []string `json:"updated,omitempty"`       // newer seed version installed
	Refreshed     []string `json:"refreshed,omitempty"`     // same version, changed files (dev builds)
	Pending       []string `json:"pending,omitempty"`       // held back: the user modified the installed copy
	LegacyWrapped []string `json:"legacyWrapped,omitempty"` // ~/.monoagent/actions/<p> folded into local-<p>
	Skipped       []string `json:"skipped,omitempty"`       // broken built-ins, skipped with the reason
}

// Changed reports whether the seed wrote anything.
func (s *SeedReport) Changed() bool {
	return len(s.Installed)+len(s.Updated)+len(s.Refreshed)+len(s.Pending)+len(s.LegacyWrapped) > 0
}

// Seed installs/updates the embedded built-ins per spec §5.1 rules. A
// broken built-in is skipped (see SeedWithReport's Skipped), never fatal.
func (r *Registry) Seed(builtins fs.FS) error {
	_, err := r.SeedWithReport(builtins)
	return err
}

type seedPkg struct {
	pkg   *Package
	files map[string][]byte
	hash  string
}

type seedOutcome int

const (
	seedSkip seedOutcome = iota
	seedInstall
	seedUpdate
	seedRefresh
	seedAdopt   // installed files already equal the seed: record its hash
	seedPending // user-modified: keep, flag the newer seed
)

// SeedWithReport is Seed returning what changed. Rules per built-in:
// removed → skip; installed version newer → skip; same version and same
// seed hash → skip; installed files unmodified since their seeding → the
// seed replaces them (new version keeps the old as previous; same version
// is refreshed in place); user-modified → kept, PendingUpdate set. A
// built-in is compared by the version it was last seeded at. When nothing
// needs to change nothing is written and no lock is taken. New or changed
// files in the legacy ~/.monoagent/actions/<p> are folded into local-<p>.
func (r *Registry) SeedWithReport(builtins fs.FS) (*SeedReport, error) {
	seeds, skipped, loadErr := loadSeeds(builtins)
	rep := &SeedReport{Skipped: skipped}
	if loadErr != nil {
		return rep, loadErr
	}
	idx, err := r.readIndex()
	if err != nil {
		return rep, err
	}
	legacy := scanLegacy(filepath.Join(r.home, "actions"))
	needed := legacyChanged(idx, legacy)
	for _, s := range seeds {
		if out, _ := r.decideSeed(idx.Packages[s.pkg.Manifest.ID], s); out != seedSkip {
			needed = true
			break
		}
	}
	if !needed {
		return rep, nil
	}
	err = r.update(func(idx *indexFile) (bool, error) {
		changed := false
		for _, s := range seeds {
			id := s.pkg.Manifest.ID
			out, pending := r.decideSeed(idx.Packages[id], s)
			switch out {
			case seedSkip:
				continue
			case seedPending:
				idx.Packages[id].PendingSeedVersion = pending
				rep.Pending = append(rep.Pending, id)
			case seedAdopt:
				e := idx.Packages[id]
				e.SeedSha256, e.InstalledSha256, e.PendingSeedVersion = s.hash, s.hash, ""
				e.SeedVersion = s.pkg.Manifest.Version
			default:
				if _, err := r.commitLocked(idx, s.pkg, s.files, true); err != nil {
					return changed, fmt.Errorf("seed %s: %w", id, err)
				}
				switch out {
				case seedInstall:
					rep.Installed = append(rep.Installed, id)
				case seedUpdate:
					rep.Updated = append(rep.Updated, id)
				case seedRefresh:
					rep.Refreshed = append(rep.Refreshed, id)
				}
			}
			changed = true
		}
		if legacyChanged(idx, legacy) {
			wrapped, err := r.foldLegacyLocked(idx, seeds, legacy)
			if err != nil {
				return changed, err
			}
			rep.LegacyWrapped = wrapped
			changed = true
		}
		return changed, nil
	})
	return rep, err
}

// decideSeed applies the seeding rules to one built-in. It hashes the
// installed files only when versions or seed hashes differ.
func (r *Registry) decideSeed(e *indexEntry, s seedPkg) (seedOutcome, string) {
	sv := s.pkg.Manifest.Version
	switch {
	case e == nil:
		return seedInstall, ""
	case e.Removed:
		return seedSkip, ""
	}
	// A built-in is compared by the version it was last seeded at, so a
	// user change ("1.0.0+local.2") never hides a newer release.
	base := e.Version
	if e.Source == SourceBuiltin && e.SeedVersion != "" {
		base = e.SeedVersion
	}
	c := CompareVersions(base, sv)
	if c > 0 || (c == 0 && e.SeedSha256 == s.hash) {
		return seedSkip, ""
	}
	cur, err := OpenDir(r.versionDir(s.pkg.Manifest.ID, e.Version))
	var h string
	if err == nil {
		h, err = fsHash(cur.FS)
	}
	if err != nil {
		// Installed files are gone or unreadable: nothing of the user's to keep.
		if c == 0 {
			return seedRefresh, ""
		}
		return seedUpdate, ""
	}
	switch {
	case h == s.hash:
		return seedAdopt, ""
	case e.Source == SourceBuiltin && e.SeedSha256 != "" && h == e.SeedSha256:
		if c == 0 {
			return seedRefresh, ""
		}
		return seedUpdate, ""
	case c == 0 && e.Source != SourceBuiltin:
		return seedSkip, "" // a user-installed copy at the shipped version
	case e.PendingSeedVersion == sv:
		return seedSkip, ""
	}
	return seedPending, sv
}

// seedRoot accepts either data.AutomationsFS (packages under
// "automations/") or an FS whose root holds the package directories.
func seedRoot(builtins fs.FS) fs.FS {
	if st, err := fs.Stat(builtins, "automations"); err == nil && st.IsDir() {
		if sub, err := fs.Sub(builtins, "automations"); err == nil {
			return sub
		}
	}
	return builtins
}

// loadSeeds opens every <id>/automation.json package of builtins. Broken
// seeds are skipped and described in skipped; err is only for an
// unreadable root.
func loadSeeds(builtins fs.FS) (seeds []seedPkg, skipped []string, err error) {
	if builtins == nil {
		return nil, nil, nil
	}
	root := seedRoot(builtins)
	entries, err := fs.ReadDir(root, ".")
	if err != nil {
		return nil, nil, fmt.Errorf("read built-in automations: %w", err)
	}
	var out []seedPkg
	var errs []error
	for _, d := range entries {
		if !d.IsDir() {
			continue
		}
		sub, err := fs.Sub(root, d.Name())
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if _, err := fs.Stat(sub, ManifestFile); err != nil {
			continue
		}
		p, err := OpenFS(sub, SourceBuiltin)
		if err != nil {
			errs = append(errs, fmt.Errorf("built-in %s: %w", d.Name(), err))
			continue
		}
		if p.Manifest.ID != d.Name() || !ValidID(p.Manifest.ID) || !validSemver(p.Manifest.Version) {
			errs = append(errs, fmt.Errorf("built-in %s: bad id %q or version %q", d.Name(), p.Manifest.ID, p.Manifest.Version))
			continue
		}
		files, err := readTree(sub)
		if err != nil {
			errs = append(errs, fmt.Errorf("built-in %s: %w", d.Name(), err))
			continue
		}
		out = append(out, seedPkg{pkg: p, files: files, hash: treeHash(files)})
	}
	for _, e := range errs {
		skipped = append(skipped, e.Error())
	}
	return out, skipped, nil
}

// Restore reinstalls the shipped copy of a built-in from builtins (after
// an uninstall, or to discard local edits).
func (r *Registry) Restore(id string, builtins fs.FS) error {
	seeds, _, err := loadSeeds(builtins)
	if err != nil {
		return err
	}
	for _, s := range seeds {
		if s.pkg.Manifest.ID != id {
			continue
		}
		return r.update(func(idx *indexFile) (bool, error) {
			if e, ok := idx.Packages[id]; ok && !e.Removed && e.Source != SourceBuiltin {
				return false, fmt.Errorf("automation %s is a %s package, not a built-in", id, e.Source)
			}
			if _, err := r.commitLocked(idx, s.pkg, s.files, true); err != nil {
				return false, err
			}
			idx.Packages[id].Enabled = true
			return true, nil
		})
	}
	return fmt.Errorf("automation %s: no built-in with that id", id)
}
