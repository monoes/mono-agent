package automation

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/monoes/mono-agent/internal/bot"
)

// SeedReport says what a Seed call changed (ids per outcome).
type SeedReport struct {
	Installed     []string `json:"installed,omitempty"`     // first install
	Updated       []string `json:"updated,omitempty"`       // newer seed version installed
	Refreshed     []string `json:"refreshed,omitempty"`     // same version, changed files (dev builds)
	Pending       []string `json:"pending,omitempty"`       // held back: the user modified the installed copy
	LegacyWrapped []string `json:"legacyWrapped,omitempty"` // ~/.monoagent/actions/<p> → local-<p>
}

// Changed reports whether the seed wrote anything.
func (s *SeedReport) Changed() bool {
	return len(s.Installed)+len(s.Updated)+len(s.Refreshed)+len(s.Pending)+len(s.LegacyWrapped) > 0
}

// Seed installs/updates the embedded built-ins per spec §5.1 rules.
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
// is refreshed in place); user-modified → kept, PendingUpdate set. When
// nothing needs to change nothing is written and no lock is taken. The
// legacy ~/.monoagent/actions directory is wrapped once.
func (r *Registry) SeedWithReport(builtins fs.FS) (*SeedReport, error) {
	seeds, loadErr := loadSeeds(builtins)
	rep := &SeedReport{}
	idx, err := r.readIndex()
	if err != nil {
		return rep, err
	}
	needed := !idx.LegacyWrapped
	for _, s := range seeds {
		if out, _ := r.decideSeed(idx.Packages[s.pkg.Manifest.ID], s); out != seedSkip {
			needed = true
			break
		}
	}
	if !needed {
		return rep, loadErr
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
		if !idx.LegacyWrapped {
			wrapped, err := r.wrapLegacyLocked(idx, seeds)
			if err != nil {
				return changed, err
			}
			rep.LegacyWrapped = wrapped
			idx.LegacyWrapped = true
			changed = true
		}
		return changed, nil
	})
	if err != nil {
		return rep, err
	}
	return rep, loadErr
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
	c := CompareVersions(e.Version, sv)
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
// seeds are skipped and reported in the joined error.
func loadSeeds(builtins fs.FS) ([]seedPkg, error) {
	if builtins == nil {
		return nil, nil
	}
	root := seedRoot(builtins)
	entries, err := fs.ReadDir(root, ".")
	if err != nil {
		return nil, fmt.Errorf("read built-in automations: %w", err)
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
	return out, errors.Join(errs...)
}

// Restore reinstalls the shipped copy of a built-in from builtins (after
// an uninstall, or to discard local edits).
func (r *Registry) Restore(id string, builtins fs.FS) error {
	seeds, _ := loadSeeds(builtins)
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

var nonSlug = regexp.MustCompile(`[^a-z0-9-]+`)

// wrapLegacyLocked wraps ~/.monoagent/actions/<p>/*.json into generated
// local-<p> packages. Nothing is deleted; runs once (index marker).
func (r *Registry) wrapLegacyLocked(idx *indexFile, seeds []seedPkg) ([]string, error) {
	legacy := filepath.Join(r.home, "actions")
	dirs, err := os.ReadDir(legacy)
	if err != nil {
		return nil, nil
	}
	var wrapped []string
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		platform := strings.ToLower(d.Name())
		id := strings.Trim(nonSlug.ReplaceAllString("local-"+platform, "-"), "-")
		if len(id) > 41 {
			id = id[:41]
		}
		if !ValidID(id) {
			continue
		}
		if _, exists := idx.Packages[id]; exists {
			continue
		}
		files := map[string][]byte{}
		var actions []string
		entries, _ := os.ReadDir(filepath.Join(legacy, d.Name()))
		for _, f := range entries {
			name := strings.TrimSuffix(f.Name(), ".json")
			if !f.Type().IsRegular() || !strings.HasSuffix(f.Name(), ".json") || !safeName(name) {
				continue
			}
			b, err := os.ReadFile(filepath.Join(legacy, d.Name(), f.Name()))
			if err != nil || !json.Valid(b) {
				continue
			}
			files["actions/"+name+".json"] = b
			actions = append(actions, name)
		}
		if len(actions) == 0 {
			continue
		}
		m := Manifest{
			Schema: SchemaV1, ID: id, Name: "Local " + d.Name(), Version: "1.0.0",
			Description: fmt.Sprintf("Legacy actions from ~/.monoagent/actions/%s (wrapped automatically)", d.Name()),
			Permissions: Permissions{Steps: []string{}, Scripts: []string{}},
			Site:        Site{Domains: []string{}},
			Actions:     actions,
			Policy:      Policy{Tier: "standard"},
		}
		for _, s := range seeds {
			if s.pkg.Manifest.Requires.Native == platform || s.pkg.Manifest.ID == platform {
				m.Requires.Native = s.pkg.Manifest.Requires.Native
				m.Site, m.Policy = s.pkg.Manifest.Site, s.pkg.Manifest.Policy
			}
		}
		if _, ok := bot.PlatformRegistry[strings.ToUpper(platform)]; ok && m.Requires.Native == "" {
			m.Requires.Native = platform
		}
		b, err := json.MarshalIndent(m, "", "  ")
		if err != nil {
			return wrapped, err
		}
		files[ManifestFile] = append(b, '\n')
		p, err := OpenFS(mapFS(files), SourceLocal)
		if err != nil {
			return wrapped, err
		}
		if _, err := r.commitLocked(idx, p, files, false); err != nil {
			return wrapped, fmt.Errorf("wrap legacy %s: %w", d.Name(), err)
		}
		wrapped = append(wrapped, id)
	}
	return wrapped, nil
}
