package automation

import (
	"fmt"
	"strings"
)

// trustRank orders tiers from least to most trusted; unknown is least.
func trustRank(t string) int {
	switch t {
	case TrustBuiltin:
		return 3
	case TrustLocal:
		return 2
	case TrustRecorded:
		return 1
	}
	return 0
}

func validTrust(t string) bool {
	switch t {
	case TrustBuiltin, TrustLocal, TrustRecorded, TrustImported:
		return true
	}
	return false
}

// lowerTrust returns the less trusted of a and b (merging content of two
// tiers yields the weaker one).
func lowerTrust(a, b string) string {
	if trustRank(a) <= trustRank(b) {
		return normTrust(a)
	}
	return normTrust(b)
}

// normTrust maps unknown values to imported (fail closed).
func normTrust(t string) string {
	if validTrust(t) {
		return t
	}
	return TrustImported
}

func trustFor(source string) string {
	switch source {
	case SourceBuiltin:
		return TrustBuiltin
	case SourceLocal:
		return TrustLocal
	}
	return TrustImported
}

// scriptsAllowed: an explicit user choice wins; otherwise builtin and local
// packages may run scripts and recorded/imported ones may not.
func scriptsAllowed(trust string, flag *bool) bool {
	if flag != nil {
		return *flag
	}
	return trustRank(trust) >= trustRank(TrustLocal)
}

// liveRunConfirmed: only imported packages need the one-time confirmation.
func liveRunConfirmed(trust string, flag bool) bool {
	return normTrust(trust) != TrustImported || flag
}

// SetTrustFlags records the user's choices for a package: scripts allows
// page_script/http_fetch_in_page for recorded/imported packages (or denies
// them for any package); live confirms real runs of an imported package's
// write-level actions. nil leaves a flag unchanged.
func (r *Registry) SetTrustFlags(id string, scripts, live *bool) error {
	return r.update(func(idx *indexFile) (bool, error) {
		e, ok := idx.Packages[id]
		if !ok || e.Removed {
			return false, fmt.Errorf("%w: %s", ErrNotInstalled, id)
		}
		changed := false
		if scripts != nil {
			v := *scripts
			e.ScriptsAllowed = &v
			changed = true
		}
		if live != nil && e.LiveRunConfirmed != *live {
			e.LiveRunConfirmed = *live
			changed = true
		}
		return changed, nil
	})
}

// --- PackageContext trust interfaces (contracts §8) -----------------------

// Trust is the package's trust tier; a package opened outside a registry
// gets its Source's tier unless Package.Trust was set.
func (c *pkgContext) Trust() string { return c.pkg.trust() }

// CallActions is manifest permissions.callActions.
func (c *pkgContext) CallActions() []string {
	return append([]string(nil), c.pkg.Manifest.Permissions.CallActions...)
}

// ScriptsAllowed reports whether page_script/http_fetch_in_page may run.
func (c *pkgContext) ScriptsAllowed() bool { return scriptsAllowed(c.pkg.trust(), c.pkg.scriptsFlag) }

// LiveRunConfirmed reports whether write-level actions may run for real.
func (c *pkgContext) LiveRunConfirmed() bool { return liveRunConfirmed(c.pkg.trust(), c.pkg.liveFlag) }

func (p *Package) trust() string {
	if p.Trust != "" {
		return normTrust(p.Trust)
	}
	return trustFor(p.Source)
}

// validCallActionRef reports whether ref is "<automation>.<action>".
func validCallActionRef(ref string) bool {
	id, name, ok := strings.Cut(ref, ".")
	return ok && ValidID(id) && safeName(name) && !strings.Contains(name, ".")
}
