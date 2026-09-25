package automation

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/action"
)

// ErrConflict is returned by AddAction when merging would overwrite a
// fragment, script, selector or other action of the target package with
// different content that another action of the target still uses.
var ErrConflict = errors.New("automation: add-action conflicts with existing package content")

// AddAction merges one action (and its fragment/selector/script closure)
// from src into installed package id. When id is not installed, src's
// manifest (cut to that action) creates it with opts.Source (default local,
// imported for an .mpkg source) and opts.Trust.
//
// Merging into a built-in keeps it a built-in: the source stays builtin,
// the version becomes "<seedVersion>+local.N" (the seed rules compare
// against the seeded version, so a newer release is flagged pending rather
// than lost) and the package reports Modified. Other packages get a patch
// bump. The merge, version bump and write all happen under the registry
// lock, so concurrent AddActions do not lose each other's work.
func (r *Registry) AddAction(id string, src *Package, actionName string, opts InstallOptions) (*InstallResult, error) {
	if !ValidID(id) {
		return nil, fmt.Errorf("automation: invalid id %q", id)
	}
	if err := checkPin(src.sha256, opts.ExpectSHA256); err != nil {
		return nil, err
	}
	if opts.Trust != "" && !validTrust(opts.Trust) {
		return nil, fmt.Errorf("automation: invalid trust %q", opts.Trust)
	}
	if opts.Trust == TrustBuiltin || opts.Source == SourceBuiltin {
		return nil, errors.New("automation: built-in packages come only from the app itself (automation restore)")
	}
	incoming := opts.Trust
	switch {
	case incoming != "":
	case opts.Source != "":
		incoming = trustFor(opts.Source)
	default:
		incoming = src.trust()
	}
	srcFiles, err := readTree(src.FS)
	if err != nil {
		return nil, err
	}
	sub, err := subsetFiles(src, srcFiles, []string{actionName})
	if err != nil {
		return nil, err
	}
	subPkg, err := OpenFS(mapFS(sub), src.Source)
	if err != nil {
		return nil, err
	}

	var res *InstallResult
	err = r.update(func(idx *indexFile) (bool, error) {
		var merged map[string][]byte
		var m Manifest
		var trust, source string
		if e, ok := idx.Packages[id]; ok && !e.Removed {
			cur, err := OpenDir(r.versionDir(id, e.Version))
			if err != nil {
				return false, fmt.Errorf("automation %s: %w", id, err)
			}
			cur.Source, cur.Trust, cur.reg = e.Source, e.trust(), r
			if merged, err = mergeInto(cur, subPkg, sub, actionName); err != nil {
				return false, err
			}
			m = mergeManifest(cur.Manifest, subPkg.Manifest)
			source, trust = e.Source, lowerTrust(e.trust(), incoming)
			if e.Source == SourceBuiltin {
				m.Version = localBuiltinVersion(e)
			} else {
				m.Version = bumpPatch(cur.Manifest.Version)
			}
		} else {
			source = opts.Source
			if source == "" {
				source = SourceLocal
				if src.Source == SourceImported {
					source = SourceImported
				}
			}
			trust = incoming
			merged = map[string][]byte{}
			for n, b := range sub {
				if !strings.HasPrefix(n, "recordings/") {
					merged[n] = b
				}
			}
			m = subPkg.Manifest
			m.ID = id
		}
		b, err := json.MarshalIndent(m, "", "  ")
		if err != nil {
			return false, err
		}
		merged[ManifestFile] = append(b, '\n')
		p, err := OpenFS(mapFS(merged), source)
		if err != nil {
			return false, err
		}
		p.Trust = trust
		res, err = r.prepareLocked(idx, p, merged, opts, true)
		if err != nil || opts.DryRun {
			return false, err
		}
		if res.Dir, err = r.commitLocked(idx, p, merged, false); err != nil {
			return false, err
		}
		res.Installed = true
		return true, nil
	})
	if res != nil {
		res.SHA256 = src.sha256
	}
	return res, err
}

// localBuiltinVersion is "<seedVersion>+local.N" for a user change to a
// built-in, N one more than the current local build of the same base.
func localBuiltinVersion(e *indexEntry) string {
	base := e.SeedVersion
	if base == "" {
		base = e.Version
	}
	if i := strings.IndexByte(base, '+'); i >= 0 {
		base = base[:i]
	}
	n := 1
	if cb, meta, ok := strings.Cut(e.Version, "+local."); ok && cb == base {
		if k, err := strconv.Atoi(meta); err == nil {
			n = k + 1
		}
	}
	return fmt.Sprintf("%s+local.%d", base, n)
}

// mergeInto returns cur's files with sub (the closure of actionName) merged
// in. Items that differ and are still used by cur's other actions are
// conflicts; so are other actions and their forms/tests. README, icon and
// recordings of the source are not merged.
func mergeInto(cur, subPkg *Package, sub map[string][]byte, actionName string) (map[string][]byte, error) {
	merged, err := readTree(cur.FS)
	if err != nil {
		return nil, err
	}
	var others []string
	for _, a := range cur.Manifest.Actions {
		if a != actionName {
			others = append(others, a)
		}
	}
	used := usage(cur, others)
	var conflicts []string
	for n, b := range sub {
		dir, base := path.Split(n)
		stem := strings.TrimSuffix(base, path.Ext(base))
		var inUse bool
		switch {
		case n == ManifestFile || n == "selectors.json":
			continue
		case dir == "actions/", dir == "forms/":
			inUse = stem != actionName
		case dir == "fragments/":
			inUse = used.fragments[stem]
		case dir == "scripts/":
			inUse = used.scripts[base]
		case strings.HasPrefix(n, "tests/"):
			inUse = !strings.HasPrefix(base, actionName+".") && !strings.HasPrefix(base, actionName+"_")
		default:
			continue // README, icon, recordings: keep the target's own
		}
		if old, exists := merged[n]; exists {
			if sameContent(n, old, b) {
				continue // equal after canonicalising: keep the target's bytes
			}
			if inUse {
				conflicts = append(conflicts, n)
				continue
			}
		}
		merged[n] = b
	}

	sel, err := cur.Selectors()
	if err != nil {
		return nil, err
	}
	add, err := subPkg.Selectors()
	if err != nil {
		return nil, err
	}
	for k, e := range add {
		old, exists := sel[k]
		switch {
		case !exists:
			sel[k] = e
		case sameSelector(old, e):
			sel[k] = newerVerified(old, e) // only verifiedAt may differ
		case used.selectors[k]:
			conflicts = append(conflicts, "selectors.json#"+k)
		default:
			sel[k] = e
		}
	}
	if len(conflicts) > 0 {
		sort.Strings(conflicts)
		return nil, fmt.Errorf("%w: %s differ from what the package's other actions use (rename them in the source): %s",
			ErrConflict, pluralItems(len(conflicts)), strings.Join(conflicts, ", "))
	}
	if len(sel) > 0 {
		b, err := json.MarshalIndent(sel, "", "  ")
		if err != nil {
			return nil, err
		}
		merged["selectors.json"] = append(b, '\n')
	}
	return merged, nil
}

// sameSelector compares two selector entries ignoring verifiedAt, which
// records when a copy was last checked, not what it selects.
func sameSelector(a, b action.SelectorEntry) bool {
	a.VerifiedAt, b.VerifiedAt = "", ""
	return reflect.DeepEqual(a, b)
}

// newerVerified returns whichever entry was verified more recently.
func newerVerified(a, b action.SelectorEntry) action.SelectorEntry {
	ta, errA := time.Parse(time.RFC3339, a.VerifiedAt)
	tb, errB := time.Parse(time.RFC3339, b.VerifiedAt)
	switch {
	case errB != nil:
		return a
	case errA != nil || tb.After(ta):
		return b
	}
	return a
}

// sameContent compares two versions of a package file: JSON files by their
// canonical form (key order and whitespace do not matter), others byte for
// byte.
func sameContent(name string, a, b []byte) bool {
	if string(a) == string(b) {
		return true
	}
	if path.Ext(name) != ".json" {
		return false
	}
	ca, errA := canonicalJSON(a)
	cb, errB := canonicalJSON(b)
	return errA == nil && errB == nil && string(ca) == string(cb)
}

func canonicalJSON(b []byte) ([]byte, error) {
	var v any
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return json.Marshal(v) // maps marshal with sorted keys
}

func pluralItems(n int) string {
	if n == 1 {
		return "1 item"
	}
	return fmt.Sprintf("%d items", n)
}

// usage is the union of the closures of actions, skipping any that do not
// resolve (a broken action should not block merging an unrelated one).
func usage(p *Package, actions []string) *closureSet {
	all := &closureSet{actions: map[string]bool{}, fragments: map[string]bool{}, selectors: map[string]bool{}, scripts: map[string]bool{}}
	for _, a := range actions {
		c, err := closure(p, []string{a})
		if err != nil {
			continue
		}
		for _, pair := range []struct{ dst, src map[string]bool }{
			{all.actions, c.actions}, {all.fragments, c.fragments}, {all.selectors, c.selectors}, {all.scripts, c.scripts},
		} {
			for k := range pair.src {
				pair.dst[k] = true
			}
		}
	}
	return all
}

// mergeManifest widens base with add's actions, permissions, scripts,
// required fragments and domains (the review's Changes shows the widening).
func mergeManifest(base, add Manifest) Manifest {
	union := func(a, b []string) []string {
		out := append([]string{}, a...)
		for _, s := range b {
			if !contains(out, s) {
				out = append(out, s)
			}
		}
		return out
	}
	base.Actions = union(base.Actions, add.Actions)
	// An empty list means unrestricted: widening it would restrict it.
	if len(base.Site.Domains) > 0 {
		base.Site.Domains = union(base.Site.Domains, add.Site.Domains)
	}
	if len(base.Permissions.Steps) > 0 {
		base.Permissions.Steps = union(base.Permissions.Steps, add.Permissions.Steps)
	}
	base.Permissions.Scripts = union(base.Permissions.Scripts, add.Permissions.Scripts)
	if len(add.Permissions.CallActions) > 0 {
		base.Permissions.CallActions = union(base.Permissions.CallActions, add.Permissions.CallActions)
	}
	base.Permissions.Downloads = base.Permissions.Downloads || add.Permissions.Downloads
	if len(add.Requires.Fragments) > 0 {
		base.Requires.Fragments = union(base.Requires.Fragments, add.Requires.Fragments)
	}
	return base
}
