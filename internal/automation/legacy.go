package automation

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/monoes/mono-agent/internal/bot"
)

// Legacy actions (~/.monoagent/actions/<p>/*.json, from `action template
// install` before packages) are folded into generated local-<p> packages.
// Every Seed rescans the directory and compares a per-platform hash with
// the one recorded in the index, so new or edited files are picked up
// while an unchanged directory costs only a read. Nothing is deleted.

type legacyDir struct {
	name    string            // directory name as found
	actions map[string][]byte // action name → JSON
	hash    string
}

var nonSlug = regexp.MustCompile(`[^a-z0-9-]+`)

func legacyID(platform string) string {
	id := strings.Trim(nonSlug.ReplaceAllString("local-"+strings.ToLower(platform), "-"), "-")
	if len(id) > 41 {
		id = id[:41]
	}
	return id
}

// scanLegacy reads every <dir>/<p>/*.json (valid JSON, plain names), keyed
// by lower-case platform.
func scanLegacy(dir string) map[string]*legacyDir {
	out := map[string]*legacyDir{}
	dirs, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, d := range dirs {
		if !d.IsDir() || !ValidID(legacyID(d.Name())) {
			continue
		}
		ld := &legacyDir{name: d.Name(), actions: map[string][]byte{}}
		entries, _ := os.ReadDir(filepath.Join(dir, d.Name()))
		for _, f := range entries {
			name := strings.TrimSuffix(f.Name(), ".json")
			if !f.Type().IsRegular() || !strings.HasSuffix(f.Name(), ".json") || !safeName(name) {
				continue
			}
			b, err := os.ReadFile(filepath.Join(dir, d.Name(), f.Name()))
			if err != nil || !json.Valid(b) {
				continue
			}
			ld.actions[name] = b
		}
		if len(ld.actions) == 0 {
			continue
		}
		files := map[string][]byte{}
		for n, b := range ld.actions {
			files["actions/"+n+".json"] = b
		}
		ld.hash = treeHash(files)
		out[strings.ToLower(d.Name())] = ld
	}
	return out
}

// legacyChanged reports whether any legacy directory differs from what was
// last folded (or one recorded before has gone).
func legacyChanged(idx *indexFile, legacy map[string]*legacyDir) bool {
	if len(legacy) != len(idx.LegacyHashes) {
		return true
	}
	for p, ld := range legacy {
		if idx.LegacyHashes[p] != ld.hash {
			return true
		}
	}
	return false
}

// foldLegacyLocked folds changed legacy directories into local-<p>: new and
// changed action files are added to the installed package (patch bump), or
// the package is created. A local-<p> the user uninstalled comes back only
// when its legacy files change. The caller holds the lock.
func (r *Registry) foldLegacyLocked(idx *indexFile, seeds []seedPkg, legacy map[string]*legacyDir) ([]string, error) {
	if idx.LegacyHashes == nil {
		idx.LegacyHashes = map[string]string{}
	}
	for p := range idx.LegacyHashes {
		if _, ok := legacy[p]; !ok {
			delete(idx.LegacyHashes, p)
		}
	}
	platforms := make([]string, 0, len(legacy))
	for p := range legacy {
		platforms = append(platforms, p)
	}
	sort.Strings(platforms)
	var folded []string
	for _, platform := range platforms {
		ld := legacy[platform]
		if idx.LegacyHashes[platform] == ld.hash {
			continue
		}
		id := legacyID(platform)
		changed, err := r.foldOneLocked(idx, id, platform, ld, seeds)
		if err != nil {
			return folded, fmt.Errorf("fold legacy %s: %w", ld.name, err)
		}
		idx.LegacyHashes[platform] = ld.hash
		if changed {
			folded = append(folded, id)
		}
	}
	return folded, nil
}

func (r *Registry) foldOneLocked(idx *indexFile, id, platform string, ld *legacyDir, seeds []seedPkg) (bool, error) {
	names := make([]string, 0, len(ld.actions))
	for n := range ld.actions {
		names = append(names, n)
	}
	sort.Strings(names)

	var files map[string][]byte
	var m Manifest
	var curHash string
	trust := TrustLocal
	if e, ok := idx.Packages[id]; ok && !e.Removed {
		cur, err := OpenDir(r.versionDir(id, e.Version))
		if err != nil {
			return false, err
		}
		if files, err = readTree(cur.FS); err != nil {
			return false, err
		}
		curHash = treeHash(files)
		m = cur.Manifest
		m.Actions = append([]string{}, m.Actions...)
		trust = lowerTrust(e.trust(), TrustLocal)
	} else {
		files = map[string][]byte{}
		m = Manifest{
			Schema: SchemaV1, ID: id, Name: "Local " + ld.name, Version: "1.0.0",
			Description: fmt.Sprintf("Legacy actions from ~/.monoagent/actions/%s (wrapped automatically)", ld.name),
			Permissions: Permissions{Steps: []string{}, Scripts: []string{}},
			Site:        Site{Domains: []string{}},
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
	}
	for _, n := range names {
		files["actions/"+n+".json"] = ld.actions[n]
		if !contains(m.Actions, n) {
			m.Actions = append(m.Actions, n)
		}
	}
	encode := func() error {
		b, err := json.MarshalIndent(m, "", "  ")
		if err != nil {
			return err
		}
		files[ManifestFile] = append(b, '\n')
		return nil
	}
	if err := encode(); err != nil {
		return false, err
	}
	if curHash != "" {
		if treeHash(files) == curHash {
			return false, nil // already folded
		}
		m.Version = bumpPatch(m.Version)
		if err := encode(); err != nil {
			return false, err
		}
	}
	p, err := OpenFS(mapFS(files), SourceLocal)
	if err != nil {
		return false, err
	}
	p.Trust = trust
	if _, err := r.commitLocked(idx, p, files, false); err != nil {
		return false, err
	}
	return true, nil
}
