package automation

import (
	"fmt"
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
		return nil, err
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
