package automation

import (
	"fmt"
	"strings"
	"sync"

	"github.com/monoes/mono-agent/internal/action"
)

// pkgContext implements action.PackageContext over a Package. Selectors are
// parsed once; the local overlay (healed selectors) wins over the package's
// own entry.
type pkgContext struct {
	pkg     *Package
	overlay map[string]overlayEntry

	once      sync.Once
	selectors map[string]action.SelectorEntry
}

var (
	_ action.PackageContext = (*pkgContext)(nil)
	_ action.DownloadsGate  = (*pkgContext)(nil)
	_ action.NativeBacked   = (*pkgContext)(nil)
)

func (c *pkgContext) ID() string                         { return c.pkg.Manifest.ID }
func (c *pkgContext) StartURL() string                   { return c.pkg.Manifest.Site.StartURL }
func (c *pkgContext) Domains() []string                  { return c.pkg.Manifest.Site.Domains }
func (c *pkgContext) PermittedSteps() []string           { return c.pkg.Manifest.Permissions.Steps }
func (c *pkgContext) Script(name string) (string, error) { return c.pkg.Script(name) }

// Form returns forms/<action>.json (the workflow editor's form override).
func (c *pkgContext) Form(actionName string) ([]byte, error) { return c.pkg.Form(actionName) }

// LegacyPlatform is the original ~/.monoagent/actions/<platform> name of a
// generated legacy package ("google_maps"), "" otherwise (Package.LegacyAlias).
func (c *pkgContext) LegacyPlatform() string { return c.pkg.LegacyAlias() }

// Native implements action.NativeBacked: requires.native of the package.
func (c *pkgContext) Native() string { return c.pkg.Manifest.Requires.Native }

func (c *pkgContext) Fragment(name string) (*action.FragmentDef, error) {
	return c.pkg.Fragment(name)
}

func (c *pkgContext) Selector(key string) (*action.SelectorEntry, bool) {
	c.once.Do(func() {
		c.selectors, _ = c.pkg.Selectors()
	})
	var base *action.SelectorEntry
	if e, ok := c.selectors[key]; ok {
		base = &e
	}
	if o, ok := c.overlay[key]; ok {
		eff, _, _ := applyOverlay(base, o)
		return eff, eff != nil
	}
	return base, base != nil
}

// ResolveAction resolves "<action>" in this package, or "<automation>.<action>"
// in another installed, enabled and available package of the same registry.
func (c *pkgContext) ResolveAction(ref string) (*action.ActionDef, action.PackageContext, error) {
	id, name, cross := strings.Cut(ref, ".")
	if !cross {
		name, id = id, c.ID()
	}
	if id == c.ID() {
		def, err := c.pkg.Action(name)
		if err != nil {
			return nil, nil, err
		}
		return def, c, nil
	}
	reg := c.pkg.reg
	if reg == nil {
		return nil, nil, fmt.Errorf("call_action %q: calling another automation only works from an installed automation, and %s is not installed", ref, c.ID())
	}
	info, err := reg.Info(id)
	if err != nil {
		return nil, nil, fmt.Errorf("call_action %q: %w", ref, err)
	}
	if !info.Enabled || !info.Available {
		reason := info.UnavailableReason
		if reason == "" {
			reason = "disabled"
		}
		return nil, nil, fmt.Errorf("call_action %q: automation %s is not usable (%s)", ref, id, reason)
	}
	other, err := reg.Get(id)
	if err != nil {
		return nil, nil, fmt.Errorf("call_action %q: %w", ref, err)
	}
	def, err := other.Action(name)
	if err != nil {
		return nil, nil, err
	}
	return def, other.Context(), nil
}
