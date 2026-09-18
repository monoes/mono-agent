package monomind

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Optional capabilities the org × workflow unification builds on
// (docs/plans/2026-09-16-org-unification-contracts.md §1). Unlike
// RequiredCapabilities, a missing one only disables the feature that needs
// it.
const (
	CapOrgToolProviders       = "org-tool-providers"       // M1 + M3: grants, boss/parent deciders, live org send
	CapOrgEndpointRoles       = "org-endpoint-roles"       // M2: automation roles
	CapOrgFederation          = "org-federation"           // M4: cross-root restrictions enforced
	CapOrgDecisionAttribution = "org-decision-attribution" // M5: --by, request-scoped approvals
)

// ErrFeatureNeedsMonomind reports that the installed monomind lacks a
// capability a feature needs.
type ErrFeatureNeedsMonomind struct {
	Capability string
	Feature    string
	Installed  string // installed version, "" when unknown
}

func (e *ErrFeatureNeedsMonomind) Error() string {
	installed := e.Installed
	if installed == "" {
		installed = "unknown"
	}
	return fmt.Sprintf("%s needs monomind with capability %q (installed: %s) — update monomind: `npm install -g @monoes/monomindcli@latest`",
		e.Feature, e.Capability, installed)
}

// CapabilitySet is the result of one handshake.
type CapabilitySet struct {
	Version string
	caps    map[string]bool
}

// Has reports whether cap was advertised.
func (c *CapabilitySet) Has(cap string) bool { return c != nil && c.caps[cap] }

// List returns the advertised capabilities.
func (c *CapabilitySet) List() []string {
	if c == nil {
		return nil
	}
	out := make([]string, 0, len(c.caps))
	for k := range c.caps {
		out = append(out, k)
	}
	return out
}

// Require returns ErrFeatureNeedsMonomind when cap is missing.
func (c *CapabilitySet) Require(cap, feature string) error {
	if c.Has(cap) {
		return nil
	}
	v := ""
	if c != nil {
		v = c.Version
	}
	return &ErrFeatureNeedsMonomind{Capability: cap, Feature: feature, Installed: v}
}

// capabilityTTL bounds how long a handshake result is reused inside one
// long-running process (the daemon), so a monomind upgrade is noticed
// without a restart.
const capabilityTTL = 5 * time.Minute

var capCache struct {
	sync.Mutex
	set *CapabilitySet
	at  time.Time
}

// Capabilities runs (or reuses a recent) handshake and returns the
// advertised capability set. An error means monomind is missing or
// unusable.
func Capabilities(ctx context.Context) (*CapabilitySet, error) {
	capCache.Lock()
	defer capCache.Unlock()
	if capCache.set != nil && time.Since(capCache.at) < capabilityTTL {
		return capCache.set, nil
	}
	_, vi, err := Ensure(ctx)
	if err != nil {
		return nil, err
	}
	set := &CapabilitySet{Version: vi.Version, caps: map[string]bool{}}
	for _, c := range vi.Capabilities {
		set.caps[c] = true
	}
	capCache.set, capCache.at = set, time.Now()
	return set, nil
}

// ResetCapabilityCache drops the cached handshake (tests, and after a
// monomind upgrade is detected).
func ResetCapabilityCache() {
	capCache.Lock()
	capCache.set = nil
	capCache.Unlock()
}

// NewCapabilitySet builds a set directly (tests and callers that already
// hold a VersionInfo).
func NewCapabilitySet(version string, caps ...string) *CapabilitySet {
	set := &CapabilitySet{Version: version, caps: map[string]bool{}}
	for _, c := range caps {
		set.caps[c] = true
	}
	return set
}
