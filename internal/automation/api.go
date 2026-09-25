package automation

import (
	"errors"
	"io"
	"io/fs"

	"github.com/monoes/mono-agent/internal/action"
)

// ErrNotImplemented marks skeleton functions not yet filled in.
var ErrNotImplemented = errors.New("automation: not implemented")

// Package is one opened automation package (any form).
type Package struct {
	Manifest Manifest
	FS       fs.FS  // rooted at the package directory
	Source   string // builtin | imported | local
	Dir      string // on-disk dir when installed ("" for zip/embed)
}

// OpenDir opens a package directory. OpenFile opens a .mpkg (zip) file.
// OpenFS opens a package rooted at fsys.
func OpenDir(dir string) (*Package, error)                       { return nil, ErrNotImplemented }
func OpenFile(path string) (*Package, error)                     { return nil, ErrNotImplemented }
func OpenFS(fsys fs.FS, source string) (*Package, error)         { return nil, ErrNotImplemented }
func (p *Package) Action(name string) (*action.ActionDef, error) { return nil, ErrNotImplemented }
func (p *Package) Context() action.PackageContext                { return nil }

// Pack writes the package directory dir as a .mpkg zip (with CHECKSUMS) to w.
func Pack(dir string, w io.Writer) error { return ErrNotImplemented }

// Validate validates a package: manifest, every action (action.Validate),
// fragments, selectors, scripts declared ↔ present, fixture tests.
func Validate(p *Package) []IssueJSON { return nil }

// PolicyAllows is the social gate (spec §6.4).
func PolicyAllows(m Manifest) (bool, string) { return true, "" }

// Registry is the installed set under <home>/automations.
type Registry struct {
	home string
}

// Open opens (creating if needed) the registry under home (normally
// ~/.monoagent). Default opens it under the user's home dir.
func Open(home string) (*Registry, error) { return &Registry{home: home}, nil }
func Default() (*Registry, error)         { return nil, ErrNotImplemented }

// Seed installs/updates the embedded built-ins per spec §5.1 rules.
func (r *Registry) Seed(builtins fs.FS) error { return ErrNotImplemented }

func (r *Registry) List(includeRemoved bool) ([]InstalledInfo, error) { return nil, ErrNotImplemented }
func (r *Registry) Get(id string) (*Package, error)                   { return nil, ErrNotImplemented }
func (r *Registry) Info(id string) (*InstalledInfo, error)            { return nil, ErrNotImplemented }

// Install installs a .mpkg file, a package directory, or an http(s) URL.
func (r *Registry) Install(src string, opts InstallOptions) (*InstallResult, error) {
	return nil, ErrNotImplemented
}

// AddAction merges one action (and its fragment/selector/script closure)
// from src into installed package id, bumping its patch version. When id
// is not installed, src's manifest is used to create it (source local).
func (r *Registry) AddAction(id string, src *Package, actionName string, opts InstallOptions) (*InstallResult, error) {
	return nil, ErrNotImplemented
}

func (r *Registry) Uninstall(id string) error               { return ErrNotImplemented }
func (r *Registry) Restore(id string, builtins fs.FS) error { return ErrNotImplemented }
func (r *Registry) SetEnabled(id string, on bool) error     { return ErrNotImplemented }
func (r *Registry) Rollback(id string) error                { return ErrNotImplemented }

// Export writes package id as a .mpkg to w.
func (r *Registry) Export(id string, w io.Writer, opts ExportOptions) error { return ErrNotImplemented }

// WriteOverlaySelector records a healed selector in the local overlay
// (<dir>/../overlay/selectors.json) without touching package files.
func (r *Registry) WriteOverlaySelector(id, key string, e action.SelectorEntry) error {
	return ErrNotImplemented
}

// DefSource adapts the registry for action.SetDefSource.
func (r *Registry) DefSource() action.DefSource { return nil }
