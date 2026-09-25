package action

// Package trust rules (security contract §8). A PackageContext may implement
// the optional interfaces below; the registry implements all of them. A
// package that does not say what it is counts as imported (fail closed),
// except for ScriptsAllowed, whose absence means allowed (builtin/local and
// test packages; the registry always answers for imported ones).

import (
	"errors"
	"fmt"
	"strings"
)

// Trusted reports the package's trust tier: builtin | local | recorded |
// imported.
type Trusted interface{ Trust() string }

// CallActionsLister reports manifest permissions.callActions: the exact
// "<automation>.<action>" refs the package may call in other packages.
type CallActionsLister interface{ CallActions() []string }

// ScriptsGate reports whether page_script / http_fetch_in_page may run.
type ScriptsGate interface{ ScriptsAllowed() bool }

// LiveRunGate reports whether the user confirmed live runs of the package's
// write-level actions (imported packages).
type LiveRunGate interface{ LiveRunConfirmed() bool }

// Tiered is optionally implemented to report the manifest policy tier
// ("social" marks a social-platform package).
type Tiered interface{ Tier() string }

// ErrRefused is wrapped by every security refusal (scripts not allowed,
// upload path not allowed, call_action denied, live run not confirmed,
// downloads not permitted, a path outside the run's workdir). Like
// ErrOffDomain it is fatal: onError cannot skip or continue past it.
var ErrRefused = errors.New("refused")

// socialHosts are the social platforms of the usage policy (spec §6.4).
var socialHosts = []string{"instagram.com", "linkedin.com", "x.com", "twitter.com", "tiktok.com", "facebook.com", "threads.net"}

// PackageTrust returns p's trust tier, "imported" when p does not say.
func PackageTrust(p PackageContext) string {
	if t, ok := p.(Trusted); ok {
		if s := strings.ToLower(strings.TrimSpace(t.Trust())); s != "" {
			return s
		}
	}
	return "imported"
}

// untrustedTier reports whether tier is one that may not reach built-in or
// social packages.
func untrustedTier(tier string) bool {
	return tier != "builtin" && tier != "local"
}

// isSocialPackage reports whether p is a social-platform package: social
// policy tier, a native bot, or a domain on the social list.
func isSocialPackage(p PackageContext) bool {
	if t, ok := p.(Tiered); ok && strings.EqualFold(t.Tier(), "social") {
		return true
	}
	if nb, ok := p.(NativeBacked); ok && nb.Native() != "" {
		return true
	}
	for _, d := range p.Domains() {
		name, _ := splitHostPort(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(d)), "*."))
		for _, s := range socialHosts {
			if name == s || strings.HasSuffix(name, "."+s) {
				return true
			}
		}
	}
	return false
}

// scriptsAllowed reports whether page_script and http_fetch_in_page may run
// for the current package.
func (ae *ActionExecutor) scriptsAllowed() bool {
	if g, ok := ae.pkg.(ScriptsGate); ok {
		return g.ScriptsAllowed()
	}
	return true
}

// CallActionError is a call_action policy refusal; Code is the validation
// issue code.
type CallActionError struct {
	Code string
	Msg  string
}

func (e *CallActionError) Error() string { return e.Msg }

// Unwrap makes every call_action policy refusal an ErrRefused.
func (e *CallActionError) Unwrap() error { return ErrRefused }

// errUnresolvedAction wraps a ResolveAction failure (a warning at validate
// time: the target may be installed later; an error at run time).
var errUnresolvedAction = errors.New("unresolved call_action target")

// CheckCallAction applies the call_action rules and resolves ref from
// caller: the ref must be literal; a call into another package must be
// listed in the caller's CallActions(); an imported or recorded caller may
// never call a builtin or social-tier package. Policy refusals are
// *CallActionError; a resolution failure wraps errUnresolvedAction.
func CheckCallAction(caller PackageContext, ref string) (*ActionDef, PackageContext, error) {
	ref = strings.TrimSpace(ref)
	if caller == nil {
		return nil, nil, &CallActionError{Code: "call_action_no_package", Msg: "call_action needs an automation package"}
	}
	if ref == "" {
		return nil, nil, &CallActionError{Code: "missing_field", Msg: `call_action needs "action"`}
	}
	if isTemplate(ref) {
		return nil, nil, &CallActionError{Code: "call_action_template", Msg: fmt.Sprintf("call_action %q: the action reference must be literal, not a template", ref)}
	}
	def, target, err := caller.ResolveAction(ref)
	if err != nil || def == nil {
		if err == nil {
			err = errors.New("not found")
		}
		return nil, nil, fmt.Errorf("%w: %q: %v", errUnresolvedAction, ref, err)
	}
	if isNilPackage(target) {
		target = caller
	}
	if strings.EqualFold(target.ID(), caller.ID()) {
		return def, target, nil
	}

	full := strings.ToLower(target.ID() + "." + def.ActionType)
	listed := false
	if l, ok := caller.(CallActionsLister); ok {
		for _, a := range l.CallActions() {
			a = strings.ToLower(strings.TrimSpace(a))
			if a == strings.ToLower(ref) || a == full {
				listed = true
				break
			}
		}
	}
	if !listed {
		return nil, nil, &CallActionError{Code: "call_action_not_allowed",
			Msg: fmt.Sprintf("call_action %q: calls into %q must be listed in the manifest's permissions.callActions", ref, target.ID())}
	}
	if tier := PackageTrust(caller); untrustedTier(tier) {
		if PackageTrust(target) == "builtin" || isSocialPackage(target) {
			return nil, nil, &CallActionError{Code: "call_action_forbidden_target",
				Msg: fmt.Sprintf("call_action %q: a %s package may not call the built-in or social automation %q", ref, tier, target.ID())}
		}
	}
	return def, target, nil
}

// sideEffectRank orders sideEffects levels; unknown ranks as write.
func sideEffectRank(level string) int {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "none", "":
		return 0
	case "read":
		return 1
	case "write":
		return 2
	case "message":
		return 3
	case "destructive":
		return 4
	}
	return 2
}

// atLeastWrite reports whether level is write, message or destructive.
func atLeastWrite(level string) bool {
	return level != "" && sideEffectRank(level) >= 2
}

// DownloadsGate is optionally implemented to report manifest
// permissions.downloads.
type DownloadsGate interface{ DownloadsPermitted() bool }

// checkLiveRun refuses a live (non-safe-mode) run of a write-level action
// of a package whose live runs the user has not confirmed.
func (ae *ActionExecutor) checkLiveRun(p PackageContext, def *ActionDef) error {
	g, ok := p.(LiveRunGate)
	if !ok || ae.safeMode || def == nil || !atLeastWrite(def.SideEffects) || g.LiveRunConfirmed() {
		return nil
	}
	return fmt.Errorf("%w: automation %s: live runs of %s-level actions need confirmation: run `monoagentcli automation trust %s --live`",
		ErrRefused, p.ID(), def.SideEffects, p.ID())
}

// enterPackage switches the executor to p for a call_action body: the
// package (domains, selectors, secrets scope) and its download permission
// (DownloadsGate; a package that does not say may not download). The
// returned func restores the caller's.
func (ae *ActionExecutor) enterPackage(p PackageContext) func() {
	prevPkg, prevDL := ae.pkg, ae.downloadsAllowed
	ae.pkg = p
	dl := false
	if g, ok := p.(DownloadsGate); ok {
		dl = g.DownloadsPermitted()
	}
	ae.downloadsAllowed = dl
	return func() { ae.pkg, ae.downloadsAllowed = prevPkg, prevDL }
}
