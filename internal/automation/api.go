package automation

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// ErrNotImplemented marks functions not yet filled in.
var ErrNotImplemented = errors.New("automation: not implemented")

// ErrInvalid is wrapped by Install/AddAction when the package has
// validation errors; the returned InstallResult still carries the review
// and the issues.
var ErrInvalid = errors.New("automation: package has validation errors")

// ErrNotInstalled is returned for an id with no installed package.
var ErrNotInstalled = errors.New("automation: not installed")

// Registry is the installed set under <home>/automations:
//
//	<home>/automations/index.json
//	<home>/automations/<id>/<version>/…        current and previous version
//	<home>/automations/<id>/overlay/selectors.json
type Registry struct {
	home string
	root string

	mu sync.Mutex // serialises writers inside one process (flock covers others)
}

// Open opens (creating if needed) the registry under home (normally
// ~/.monoagent).
func Open(home string) (*Registry, error) {
	if home == "" {
		return nil, errors.New("automation: empty registry home")
	}
	abs, err := filepath.Abs(home)
	if err != nil {
		return nil, err
	}
	r := &Registry{home: abs, root: filepath.Join(abs, "automations")}
	if err := os.MkdirAll(r.root, 0o755); err != nil {
		return nil, fmt.Errorf("automation: create registry: %w", err)
	}
	return r, nil
}

// Default opens the registry under ~/.monoagent (honouring $HOME).
func Default() (*Registry, error) {
	home := os.Getenv("HOME")
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("automation: resolve home: %w", err)
		}
		home = h
	}
	return Open(filepath.Join(home, ".monoagent"))
}

// Home is the directory the registry was opened on (e.g. ~/.monoagent).
func (r *Registry) Home() string { return r.home }

// Root is <home>/automations.
func (r *Registry) Root() string { return r.root }
