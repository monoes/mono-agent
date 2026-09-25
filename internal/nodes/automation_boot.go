package nodes

import (
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/monoes/mono-agent/data"
	"github.com/monoes/mono-agent/internal/action"
	"github.com/monoes/mono-agent/internal/automation"
	"github.com/monoes/mono-agent/internal/browser"
	"github.com/rs/zerolog"
)

var (
	bootMu     sync.Mutex
	bootedHome string
	bootedReg  *automation.Registry

	autoBootOnce sync.Once
)

// BootAutomations opens the automation registry under home (normally
// ~/.monoagent; "" means automation.Default), seeds the embedded built-in
// packages and switches the action loader to the registry. It is idempotent
// per home. On error the loader is left as it was (the legacy embedded seed
// + ~/.monoagent/actions), so callers log the error and carry on.
func BootAutomations(home string) (*automation.Registry, error) {
	bootMu.Lock()
	defer bootMu.Unlock()
	if bootedReg != nil && bootedHome == home {
		return bootedReg, nil
	}

	var reg *automation.Registry
	var err error
	if home == "" {
		reg, err = automation.Default()
	} else {
		reg, err = automation.Open(home)
	}
	if err != nil {
		return nil, fmt.Errorf("open automation registry: %w", err)
	}
	if reg == nil {
		return nil, fmt.Errorf("open automation registry: no registry")
	}
	seed, err := fs.Sub(data.AutomationsFS, "automations")
	if err != nil {
		return nil, fmt.Errorf("embedded automations: %w", err)
	}
	if err := reg.Seed(seed); err != nil {
		return nil, fmt.Errorf("seed built-in automations: %w", err)
	}
	src := reg.DefSource()
	if src == nil {
		return nil, fmt.Errorf("automation registry has no definition source")
	}

	action.SetDefSource(src)
	browser.SetStartURLResolver(packageStartURL)
	bootedHome, bootedReg = home, reg
	return reg, nil
}

// ensureAutomationsBooted boots the default registry once per process. Every
// node registry is built through RegisterBrowserNodes, so this covers the
// CLI, the daemon, MCP, the HTTP API and the desktop app without each
// startup site having to remember it. Test binaries never touch the real
// home: they call BootAutomations with a temp dir explicitly.
func ensureAutomationsBooted() {
	if testing.Testing() {
		return
	}
	autoBootOnce.Do(func() {
		if _, err := BootAutomations(""); err != nil {
			l := zerolog.New(os.Stderr).With().Timestamp().Logger()
			l.Warn().Err(err).Msg("automations: registry unavailable, using the built-in action set")
		}
	})
}

// bootedManifest returns the installed manifest for an automation, when the
// registry was booted and has it.
func bootedManifest(id string) (*automation.Manifest, bool) {
	bootMu.Lock()
	reg := bootedReg
	bootMu.Unlock()
	if reg == nil {
		return nil, false
	}
	p, err := reg.Get(id)
	if err != nil || p == nil {
		return nil, false
	}
	return &p.Manifest, true
}

// nativePlatform is the compiled Go bot an automation's actions call into:
// manifest requires.native, else the automation id itself (legacy platforms,
// where the two are the same).
func nativePlatform(id string) string {
	if m, ok := bootedManifest(id); ok && m.Requires.Native != "" {
		return m.Requires.Native
	}
	return id
}

// packageStartURL resolves manifest site.startUrl through the installed
// definition source ("" when unknown).
func packageStartURL(platform string) string {
	src := action.CurrentDefSource()
	if src == nil {
		return ""
	}
	pkg := src.Package(strings.ToLower(platform))
	if pkg == nil {
		return ""
	}
	return pkg.StartURL()
}

// downloadsSetter is implemented by the executor once the action core adds
// SetDownloadsAllowed (contracts §3); until then downloads stay at the
// executor's default.
type downloadsSetter interface{ SetDownloadsAllowed(bool) }

// attachPackage hands the executor the package its action belongs to, the
// selector-health observer and the manifest's download permission. Without a
// package (legacy loader) the executor keeps its legacy behaviour.
func attachPackage(ex *action.ActionExecutor, platform string, db *sql.DB) {
	src := action.CurrentDefSource()
	if src == nil {
		return
	}
	pkg := src.Package(strings.ToLower(platform))
	if pkg == nil {
		return
	}
	ex.SetPackage(pkg)
	if db != nil {
		if obs := automation.HealthObserver(db); obs != nil {
			ex.SetSelectorObserver(obs)
		}
	}
	if ds, ok := any(ex).(downloadsSetter); ok {
		m, found := bootedManifest(pkg.ID())
		ds.SetDownloadsAllowed(found && m.Permissions.Downloads)
	}
}
