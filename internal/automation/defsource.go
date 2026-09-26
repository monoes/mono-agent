package automation

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/monoes/mono-agent/internal/action"
)

// DefSource adapts the registry for action.SetDefSource: only enabled and
// available packages are visible.
func (r *Registry) DefSource() action.DefSource { return &defSource{r: r} }

type defSource struct{ r *Registry }

func (d *defSource) usable(id string) (*Package, error) {
	e, err := d.r.entry(id)
	if err != nil {
		// Not an id: maybe the original name of a legacy platform
		// ("google_maps" → local-google-maps).
		alias, aerr := d.r.resolveLegacyAlias(id)
		if aerr != nil || alias == "" {
			return nil, err
		}
		id = alias
		if e, err = d.r.entry(id); err != nil {
			return nil, err
		}
	}
	info := d.r.info(id, e, false)
	if !info.Enabled {
		return nil, fmt.Errorf("automation %s is disabled", id)
	}
	if !info.Available {
		return nil, fmt.Errorf("automation %s is unavailable: %s", id, info.UnavailableReason)
	}
	return d.r.Get(id)
}

// Load returns the raw action JSON for automation/action.
func (d *defSource) Load(automation, actionType string) ([]byte, error) {
	p, err := d.usable(strings.ToLower(automation))
	if err != nil {
		return nil, err
	}
	name := actionType
	if !contains(p.Manifest.Actions, name) {
		// The loader lower-cases names; match the manifest case-insensitively.
		for _, a := range p.Manifest.Actions {
			if strings.EqualFold(a, actionType) {
				name = a
				break
			}
		}
	}
	return p.ActionJSON(name)
}

// List returns "<automation>/<action>" for every enabled, available package.
func (d *defSource) List() ([]string, error) {
	idx, err := d.r.readIndex()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, id := range sortedIDs(idx) {
		p, err := d.usable(id)
		if err != nil {
			continue
		}
		for _, a := range p.Manifest.Actions {
			out = append(out, id+"/"+a)
		}
	}
	return out, nil
}

// Package returns the package context for an automation, or nil.
func (d *defSource) Package(automation string) action.PackageContext {
	p, err := d.usable(strings.ToLower(automation))
	if err != nil {
		return nil
	}
	return p.Context()
}

// Generation changes whenever the index is written (install, seed, enable,
// trust flags, overlay writes, promotions, …): the index's write counter
// plus its modification time, which also covers a hand-edited index. The
// action loader uses it to drop cached definitions.
func (d *defSource) Generation() string {
	idx, err := d.r.readIndex()
	if err != nil {
		return "error"
	}
	var mod int64
	if st, err := os.Stat(filepath.Join(d.r.root, indexName)); err == nil {
		mod = st.ModTime().UnixNano()
	}
	return fmt.Sprintf("%d:%d", idx.Generation, mod)
}

// ResolveLegacyPlatform returns the id of the installed generated legacy
// package for an old platform name, enabled or not: first an exact
// (case-insensitive) match of its original name ("google_maps"), then a
// match of the slug ("google-maps" also finds the google_maps package).
func (r *Registry) ResolveLegacyPlatform(name string) (string, bool) {
	id, err := r.resolveLegacyAlias(name)
	return id, err == nil && id != ""
}

// resolveLegacyAlias implements ResolveLegacyPlatform ("" when none).
func (r *Registry) resolveLegacyAlias(name string) (string, error) {
	idx, err := r.readIndex()
	if err != nil {
		return "", err
	}
	type cand struct{ id, alias string }
	var cands []cand
	for _, id := range sortedIDs(idx) {
		e := idx.Packages[id]
		if e.Removed || e.Source != SourceLocal {
			continue
		}
		p, err := OpenDir(r.versionDir(id, e.Version))
		if err != nil {
			continue
		}
		p.Source, p.Trust = e.Source, e.trust()
		if a := p.LegacyAlias(); a != "" {
			cands = append(cands, cand{id, a})
		}
	}
	for _, c := range cands {
		if strings.EqualFold(c.alias, name) {
			return c.id, nil
		}
	}
	for _, c := range cands {
		if legacyID(c.alias) == legacyID(name) {
			return c.id, nil
		}
	}
	return "", nil
}
