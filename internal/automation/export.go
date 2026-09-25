package automation

import (
	"encoding/json"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/monoes/mono-agent/internal/action"
)

// Export writes package id as a .mpkg to w. recordings/ is left out unless
// opts.WithRecordings; the local overlay and sessions are never part of a
// package. With opts.Actions the package is cut down to those actions and
// the closure of fragments, selectors, scripts and same-package actions
// they reference, and the manifest is rewritten to match.
func (r *Registry) Export(id string, w io.Writer, opts ExportOptions) error {
	p, err := r.Get(id)
	if err != nil {
		return err
	}
	files, err := exportFiles(p, opts)
	if err != nil {
		return err
	}
	return writeZip(files, w)
}

// exportFiles returns the files of p an export writes.
func exportFiles(p *Package, opts ExportOptions) (map[string][]byte, error) {
	files, err := readTree(p.FS)
	if err != nil {
		return nil, err
	}
	if !opts.WithRecordings {
		for n := range files {
			if strings.HasPrefix(n, "recordings/") {
				delete(files, n)
			}
		}
	}
	if len(opts.Actions) == 0 {
		return files, nil
	}
	return subsetFiles(p, files, opts.Actions)
}

// closureSet is what a set of actions references inside its package.
type closureSet struct {
	actions   map[string]bool
	fragments map[string]bool
	selectors map[string]bool
	scripts   map[string]bool
}

// closure computes the transitive references of actions within p.
func closure(p *Package, actions []string) (*closureSet, error) {
	c := &closureSet{actions: map[string]bool{}, fragments: map[string]bool{}, selectors: map[string]bool{}, scripts: map[string]bool{}}
	var walk func(steps []action.StepDef) error
	addAction := func(name string) error {
		if c.actions[name] {
			return nil
		}
		def, err := p.Action(name)
		if err != nil {
			return err
		}
		c.actions[name] = true
		return walk(def.Steps)
	}
	walk = func(steps []action.StepDef) error {
		for _, s := range steps {
			if s.ConfigKey != "" {
				c.selectors[s.ConfigKey] = true
			}
			if s.Script != "" {
				name := s.Script
				if !strings.HasSuffix(name, ".js") {
					name += ".js"
				}
				c.scripts[name] = true
			}
			if s.Fragment != "" && !c.fragments[s.Fragment] {
				frag, err := p.Fragment(s.Fragment)
				if err != nil {
					return err
				}
				c.fragments[s.Fragment] = true
				if err := walk(frag.Steps); err != nil {
					return err
				}
			}
			if s.Action != "" {
				id, name, cross := strings.Cut(s.Action, ".")
				if !cross {
					name, id = id, p.Manifest.ID
				}
				if id == p.Manifest.ID {
					if err := addAction(name); err != nil {
						return err
					}
				}
			}
			if err := walk(s.Steps); err != nil {
				return err
			}
		}
		return nil
	}
	for _, a := range actions {
		if !contains(p.Manifest.Actions, a) {
			return nil, fmt.Errorf("automation %s has no action %q", p.Manifest.ID, a)
		}
		if err := addAction(a); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// subsetFiles cuts files down to the closure of actions and rewrites the
// manifest (actions, declared scripts, required fragments) to match.
func subsetFiles(p *Package, files map[string][]byte, actions []string) (map[string][]byte, error) {
	c, err := closure(p, actions)
	if err != nil {
		return nil, err
	}
	m := p.Manifest
	out := map[string][]byte{}
	for n, b := range files {
		dir, base := path.Split(n)
		stem := strings.TrimSuffix(base, path.Ext(base))
		keep := false
		switch {
		case n == ManifestFile || n == "selectors.json":
			// rewritten below
		case dir == "actions/":
			keep = c.actions[stem]
		case dir == "fragments/":
			keep = c.fragments[stem]
		case dir == "scripts/":
			keep = c.scripts[base]
		case dir == "forms/":
			keep = c.actions[stem]
		case strings.HasPrefix(n, "tests/"):
			for a := range c.actions {
				if strings.HasPrefix(base, a+".") || strings.HasPrefix(base, a+"_") {
					keep = true
				}
			}
		default:
			// icon, README, recordings (already filtered) and other docs
			keep = true
		}
		if keep {
			out[n] = b
		}
	}

	if len(c.selectors) > 0 {
		all, err := p.Selectors()
		if err != nil {
			return nil, err
		}
		sel := map[string]action.SelectorEntry{}
		for k := range c.selectors {
			if e, ok := all[k]; ok {
				sel[k] = e
			}
		}
		if len(sel) > 0 {
			b, err := json.MarshalIndent(sel, "", "  ")
			if err != nil {
				return nil, err
			}
			out["selectors.json"] = append(b, '\n')
		}
	}

	m.Actions = filterOrdered(m.Actions, c.actions)
	m.Permissions.Scripts = filterOrdered(m.Permissions.Scripts, c.scripts)
	m.Requires.Fragments = filterOrdered(m.Requires.Fragments, c.fragments)
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	out[ManifestFile] = append(b, '\n')
	return out, nil
}

func filterOrdered(list []string, keep map[string]bool) []string {
	var out []string
	for _, s := range list {
		if keep[s] {
			out = append(out, s)
		}
	}
	if out == nil && list != nil {
		out = []string{}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
