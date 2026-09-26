package automation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/net/publicsuffix"

	"github.com/monoes/mono-agent/internal/action"
	"github.com/monoes/mono-agent/internal/bot"
)

// Legacy actions (~/.monoagent/actions/<p>/*.json, from `action template
// install` before packages) are folded into generated local-<p> packages.
// Every Seed rescans the directory and compares a per-platform hash with
// the one recorded in the index, so new or edited files are picked up
// while an unchanged directory costs only a read. Nothing is deleted.
//
// The generated manifest records the original directory name
// (manifest.legacy.platform) so old "<p>.<action>" node types keep working
// even when <p> is not a valid id (google_maps → local-google-maps), and
// derives site.domains and permissions.steps from what the actions do.

// legacyFormat versions the generated manifests; a registry folded with an
// older format is refolded once on the next Seed.
const legacyFormat = 3 // 3: site.domains no longer derived (v0.74 regression)

type legacyDir struct {
	name    string            // directory name as found
	actions map[string][]byte // action name → JSON
	hash    string
}

var nonSlug = regexp.MustCompile(`[^a-z0-9-]+`)

// LegacyPackageID is the id a legacy ~/.monoagent/actions/<platform>
// directory is wrapped into when no other platform claimed it first
// ("google_maps" → "local-google-maps"). Colliding or over-long names get
// a hashed id instead, so to find an installed package prefer
// Registry.ResolveLegacyPlatform.
func LegacyPackageID(platform string) string { return legacyID(platform) }

// legacyID is the plain id for platform (truncated to the id length).
func legacyID(platform string) string {
	id := strings.Trim(nonSlug.ReplaceAllString("local-"+strings.ToLower(platform), "-"), "-")
	if len(id) > 41 {
		id = strings.TrimRight(id[:41], "-")
	}
	return id
}

// legacyHashedID disambiguates platforms whose plain ids collide (long
// names truncated alike, or google_maps vs google-maps).
func legacyHashedID(platform string) string {
	sum := sha256.Sum256([]byte(platform))
	base := legacyID(platform)
	if len(base) > 34 {
		base = strings.TrimRight(base[:34], "-")
	}
	return base + "-" + hex.EncodeToString(sum[:3])
}

// legacyIDFor picks the id for ld: the plain id unless the package
// installed there belongs to another legacy platform. A package folded
// before the platform was recorded counts as ours when it already has all
// of ld's actions.
func (r *Registry) legacyIDFor(idx *indexFile, ld *legacyDir) string {
	id := legacyID(ld.name)
	e, ok := idx.Packages[id]
	if !ok || e.Removed {
		return id
	}
	p, err := OpenDir(r.versionDir(id, e.Version))
	if err != nil {
		return id
	}
	if l := p.Manifest.Legacy; l != nil {
		if strings.EqualFold(l.Platform, ld.name) {
			return id
		}
		return legacyHashedID(ld.name)
	}
	for a := range ld.actions {
		if !contains(p.Manifest.Actions, a) {
			return legacyHashedID(ld.name)
		}
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
// last folded (or one recorded before has gone), or the generated format
// is older than this build's.
func legacyChanged(idx *indexFile, legacy map[string]*legacyDir) bool {
	if idx.LegacyFormat < legacyFormat || len(legacy) != len(idx.LegacyHashes) {
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
// when its legacy files change. After a format change every installed
// generated package is refolded once. The caller holds the lock.
func (r *Registry) foldLegacyLocked(idx *indexFile, seeds []seedPkg, legacy map[string]*legacyDir) ([]string, error) {
	if idx.LegacyHashes == nil {
		idx.LegacyHashes = map[string]string{}
	}
	refold := idx.LegacyFormat < legacyFormat
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
		id := r.legacyIDFor(idx, ld)
		e, installed := idx.Packages[id]
		installed = installed && !e.Removed
		if idx.LegacyHashes[platform] == ld.hash && !(refold && installed) {
			continue
		}
		changed, err := r.foldOneLocked(idx, id, ld, seeds)
		if err != nil {
			return folded, fmt.Errorf("fold legacy %s: %w", ld.name, err)
		}
		idx.LegacyHashes[platform] = ld.hash
		if changed {
			folded = append(folded, id)
		}
	}
	idx.LegacyFormat = legacyFormat
	return folded, nil
}

func (r *Registry) foldOneLocked(idx *indexFile, id string, ld *legacyDir, seeds []seedPkg) (bool, error) {
	platform := strings.ToLower(ld.name)
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
	m.Legacy = &LegacyInfo{Platform: ld.name}
	for _, n := range names {
		files["actions/"+n+".json"] = ld.actions[n]
		if !contains(m.Actions, n) {
			m.Actions = append(m.Actions, n)
		}
	}
	var generated []string // domains an earlier build put there, not the user
	for _, s := range seeds {
		if s.pkg.Manifest.Requires.Native == platform || s.pkg.Manifest.ID == platform {
			generated = append(generated, s.pkg.Manifest.Site.Domains...)
		}
	}
	deriveLegacyPermissions(&m, files, generated)

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

// deriveLegacyPermissions fills permissions.steps from the step types the
// actions use and records the sites their literal navigate URLs open as a
// suggestion only (legacy.suggestedDomains: each host plus the *. glob of
// its registrable domain). site.domains stays EMPTY: legacy actions ran
// unrestricted before packages existed — templated URLs, redirects to
// www. — and must keep doing so (the documented legacy exception). Domains
// the user added by hand are kept; ones an earlier build derived (exact
// hosts, a seed's list) are cleared.
func deriveLegacyPermissions(m *Manifest, files map[string][]byte, generated []string) {
	var hosts, local, steps []string
	var walk func([]action.StepDef)
	walk = func(ss []action.StepDef) {
		for _, s := range ss {
			if s.Type != "" && !contains(steps, s.Type) {
				steps = append(steps, s.Type)
			}
			if s.Type == "navigate" && !strings.Contains(s.URL, "{{") {
				if u, err := url.Parse(strings.TrimSpace(s.URL)); err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" {
					h := strings.ToLower(u.Host)
					switch {
					case checkDomainPattern(h) != nil:
						if !contains(local, h) {
							local = append(local, h)
						}
					case !contains(hosts, h):
						hosts = append(hosts, h)
					}
				}
			}
			walk(s.Steps)
		}
	}
	for _, a := range m.Actions {
		var def action.ActionDef
		if json.Unmarshal(files["actions/"+a+".json"], &def) == nil {
			walk(def.Steps)
		}
	}
	for n, b := range files {
		if strings.HasPrefix(n, "fragments/") {
			var f action.FragmentDef
			if json.Unmarshal(b, &f) == nil {
				walk(f.Steps)
			}
		}
	}
	sort.Strings(steps)
	if len(m.Permissions.Steps) > 0 || len(steps) > 0 {
		m.Permissions.Steps = unionSorted(m.Permissions.Steps, steps)
	}
	if m.Permissions.Scripts == nil {
		m.Permissions.Scripts = []string{}
	}

	var suggested []string
	for _, h := range hosts {
		suggested = append(suggested, h)
		if reg, err := publicsuffix.EffectiveTLDPlusOne(strings.Split(h, ":")[0]); err == nil {
			suggested = append(suggested, "*."+reg)
		}
	}
	sort.Strings(local)
	m.Legacy.SuggestedDomains = unionSorted(nil, suggested)
	m.Legacy.LocalHosts = local

	// Clear generated domains; keep hand-added ones.
	generated = append(append([]string{}, generated...), suggested...)
	kept := []string{}
	for _, d := range m.Site.Domains {
		if !contains(generated, d) {
			kept = append(kept, d)
		}
	}
	m.Site.Domains = kept
	if m.Site.StartURL == "" && len(hosts) > 0 {
		m.Site.StartURL = "https://" + hosts[0] + "/"
	}
	if len(kept) > 0 {
		if err := urlInDomains(m.Site.StartURL, kept); err != nil {
			m.Site.StartURL = ""
		}
	}
}

func unionSorted(a, b []string) []string {
	out := append([]string{}, a...)
	for _, s := range b {
		if !contains(out, s) {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
