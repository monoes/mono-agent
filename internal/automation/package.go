package automation

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/monoes/mono-agent/internal/action"
)

// Package is one opened automation package (any form).
type Package struct {
	Manifest Manifest
	FS       fs.FS  // rooted at the package directory
	Source   string // builtin | imported | local
	Dir      string // on-disk dir when installed ("" for zip/embed)

	reg *Registry // set when opened through a Registry (call_action lookups)
}

// OpenDir opens a package directory (source local).
func OpenDir(dir string) (*Package, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", dir)
	}
	p, err := OpenFS(os.DirFS(abs), SourceLocal)
	if err != nil {
		return nil, err
	}
	p.Dir = abs
	return p, nil
}

// OpenFile opens a .mpkg (zip) file (source imported). The archive is
// checked for unsafe entries, size caps and CHECKSUMS before anything is
// read from it.
func OpenFile(path string) (*Package, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fsys, err := readZip(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return OpenFS(fsys, SourceImported)
}

// OpenFS opens a package rooted at fsys.
func OpenFS(fsys fs.FS, source string) (*Package, error) {
	m, err := readManifest(fsys)
	if err != nil {
		return nil, err
	}
	if source == "" {
		source = SourceLocal
	}
	return &Package{Manifest: m, FS: fsys, Source: source}, nil
}

// Action loads actions/<name>.json.
func (p *Package) Action(name string) (*action.ActionDef, error) {
	b, err := p.ActionJSON(name)
	if err != nil {
		return nil, err
	}
	var def action.ActionDef
	if err := json.Unmarshal(b, &def); err != nil {
		return nil, fmt.Errorf("parse actions/%s.json: %w", name, err)
	}
	return &def, nil
}

// ActionJSON returns the raw bytes of actions/<name>.json.
func (p *Package) ActionJSON(name string) ([]byte, error) {
	if !safeName(name) {
		return nil, fmt.Errorf("invalid action name %q", name)
	}
	b, err := fs.ReadFile(p.FS, "actions/"+name+".json")
	if err != nil {
		return nil, fmt.Errorf("automation %s: action %q not found", p.Manifest.ID, name)
	}
	return b, nil
}

// Context returns the package view the action executor runs against.
func (p *Package) Context() action.PackageContext {
	c := &pkgContext{pkg: p}
	if p.reg != nil && p.Dir != "" {
		c.overlay = p.reg.readOverlay(p.Manifest.ID)
	}
	return c
}

// Selectors parses selectors.json (empty map when absent).
func (p *Package) Selectors() (map[string]action.SelectorEntry, error) {
	out := map[string]action.SelectorEntry{}
	b, err := fs.ReadFile(p.FS, "selectors.json")
	if err != nil {
		return out, nil
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("parse selectors.json: %w", err)
	}
	return out, nil
}

// Fragment parses fragments/<name>.json.
func (p *Package) Fragment(name string) (*action.FragmentDef, error) {
	if !safeName(name) {
		return nil, fmt.Errorf("invalid fragment name %q", name)
	}
	b, err := fs.ReadFile(p.FS, "fragments/"+name+".json")
	if err != nil {
		return nil, fmt.Errorf("automation %s: fragment %q not found", p.Manifest.ID, name)
	}
	var f action.FragmentDef
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("parse fragments/%s.json: %w", name, err)
	}
	if f.Name == "" {
		f.Name = name
	}
	return &f, nil
}

// Form returns the raw forms/<action>.json override. A missing file is
// an error wrapping fs.ErrNotExist.
func (p *Package) Form(actionName string) ([]byte, error) {
	if !safeName(actionName) {
		return nil, fmt.Errorf("invalid action name %q", actionName)
	}
	b, err := fs.ReadFile(p.FS, "forms/"+actionName+".json")
	if err != nil {
		return nil, fmt.Errorf("automation %s: form %q: %w", p.Manifest.ID, actionName, err)
	}
	return b, nil
}

// Script returns scripts/<name> (".js" is appended when missing).
func (p *Package) Script(name string) (string, error) {
	if !strings.HasSuffix(name, ".js") {
		name += ".js"
	}
	if !safeName(name) {
		return "", fmt.Errorf("invalid script name %q", name)
	}
	b, err := fs.ReadFile(p.FS, "scripts/"+name)
	if err != nil {
		return "", fmt.Errorf("automation %s: script %q not found", p.Manifest.ID, name)
	}
	return string(b), nil
}

// Files lists every regular file in the package (slash paths, sorted).
func (p *Package) Files() ([]string, error) { return listFiles(p.FS) }

// ScriptFiles lists the file names under scripts/.
func (p *Package) ScriptFiles() []string { return dirFiles(p.FS, "scripts", "") }

// FragmentNames lists fragments/*.json names without the extension.
func (p *Package) FragmentNames() []string { return dirFiles(p.FS, "fragments", ".json") }

// ActionFiles lists actions/*.json names without the extension.
func (p *Package) ActionFiles() []string { return dirFiles(p.FS, "actions", ".json") }

func dirFiles(fsys fs.FS, dir, ext string) []string {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") || !strings.HasSuffix(e.Name(), ext) {
			continue
		}
		out = append(out, strings.TrimSuffix(e.Name(), ext))
	}
	sort.Strings(out)
	return out
}

// listFiles walks fsys and returns every regular file, skipping dotfiles
// and dot-directories.
func listFiles(fsys fs.FS) ([]string, error) {
	var out []string
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p != "." && strings.HasPrefix(path.Base(p), ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() {
			out = append(out, p)
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}
