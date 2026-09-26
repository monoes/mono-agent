package action

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/rs/zerolog"
)

// Issue is one validation finding.
type Issue struct {
	Severity string `json:"severity"` // "error" | "warning"
	StepID   string `json:"stepId,omitempty"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}

// ValidationError is returned by Execute when the action definition has
// validation errors; Issues holds every finding (errors and warnings).
type ValidationError struct {
	Action string
	Issues []Issue
}

func (e *ValidationError) Error() string {
	var msgs []string
	for _, i := range e.Issues {
		if i.Severity != "error" {
			continue
		}
		if i.StepID != "" {
			msgs = append(msgs, fmt.Sprintf("%s: %s (%s)", i.StepID, i.Message, i.Code))
		} else {
			msgs = append(msgs, fmt.Sprintf("%s (%s)", i.Message, i.Code))
		}
	}
	return fmt.Sprintf("action %s failed validation: %s", e.Action, strings.Join(msgs, "; "))
}

// NativeBacked is optionally implemented by a PackageContext whose package
// declares requires.native (the built-in social platforms). Those actions
// keep the legacy ConfigInterface selector lookup, so a configKey need not be
// present in selectors.json.
type NativeBacked interface {
	Native() string
}

var sideEffectLevels = map[string]bool{"none": true, "read": true, "write": true, "message": true, "destructive": true}

var (
	knownStepsOnce sync.Once
	knownSteps     map[string]bool
)

// KnownStepTypes returns every step type the executor has a handler for
// (core and extended), sorted.
func KnownStepTypes() []string {
	set := knownStepSet()
	out := make([]string, 0, len(set))
	for t := range set {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

func knownStepSet() map[string]bool {
	knownStepsOnce.Do(func() {
		ae := NewActionExecutor(context.Background(), nil, nil, nil, nil, nil, zerolog.Nop())
		knownSteps = make(map[string]bool, len(ae.handlers))
		for t := range ae.handlers {
			knownSteps[t] = true
		}
	})
	return knownSteps
}

// Validate checks an action definition, optionally against the package it
// belongs to (pkg may be nil: legacy action, no package rules).
func Validate(def *ActionDef, pkg PackageContext) []Issue {
	if def == nil {
		return []Issue{{Severity: "error", Code: "nil_action", Message: "no action definition"}}
	}
	v := &validator{pkg: pkg, legacy: isLegacyPackage(pkg), known: knownStepSet(), fragDone: map[string]bool{}}
	if pkg != nil {
		if nb, ok := pkg.(NativeBacked); ok && nb.Native() != "" {
			v.native = true
		}
	}

	if len(def.Steps) == 0 {
		v.warn("", "no_steps", "action has no steps")
	}
	switch {
	case def.SideEffects == "":
		v.warn("", "no_side_effects", `action does not declare "sideEffects" (none|read|write|message|destructive)`)
	case !sideEffectLevels[def.SideEffects]:
		v.err("", "invalid_side_effects", fmt.Sprintf("sideEffects %q is not one of none|read|write|message|destructive", def.SideEffects))
	}

	v.steps("", def.Steps, def.Loops)
	if atLeastWrite(def.SideEffects) && !v.anyFlagged(def.Steps, map[string]bool{}) {
		v.err("", "unflagged_side_effect", fmt.Sprintf(`sideEffects is %q but no step is marked "sideEffect": true, so safe-mode verification would run the write`, def.SideEffects))
	}
	return v.issues
}

// anyFlagged reports whether a step (nested bodies and fragments included)
// is marked sideEffect, or is a call_action (safe mode stops before calls
// whose target writes).
func (v *validator) anyFlagged(steps []StepDef, seen map[string]bool) bool {
	for _, s := range steps {
		if s.SideEffect || s.Type == "call_action" || v.anyFlagged(s.Steps, seen) {
			return true
		}
		if s.Type == "call_fragment" && v.pkg != nil && !seen[s.Fragment] {
			seen[s.Fragment] = true
			if f, err := v.pkg.Fragment(s.Fragment); err == nil && f != nil && v.anyFlagged(f.Steps, seen) {
				return true
			}
		}
	}
	return false
}

func (v *validator) callAction(id string, s StepDef) {
	if s.Action == "" {
		v.err(id, "missing_field", `call_action needs "action"`)
		return
	}
	if isTemplate(s.Action) {
		v.err(id, "call_action_template", fmt.Sprintf("call_action %q: the action reference must be literal, not a template", s.Action))
		return
	}
	if v.pkg == nil {
		return
	}
	_, _, err := CheckCallAction(v.pkg, s.Action)
	var cae *CallActionError
	switch {
	case err == nil:
	case errors.As(err, &cae):
		v.err(id, cae.Code, cae.Msg)
	default:
		v.warn(id, "unresolved_action", err.Error())
	}
}

type validator struct {
	// legacy: no package, or a legacy-migrated local-* package. Master ran
	// such actions without step ids, so a missing id is only a warning
	// there (the executor assigns synthetic ids, see assignSyntheticIDs).
	legacy   bool
	pkg      PackageContext
	native   bool
	known    map[string]bool
	fragDone map[string]bool
	issues   []Issue
}

func (v *validator) add(sev, stepID, code, msg string) {
	v.issues = append(v.issues, Issue{Severity: sev, StepID: stepID, Code: code, Message: msg})
}
func (v *validator) err(stepID, code, msg string)  { v.add("error", stepID, code, msg) }
func (v *validator) warn(stepID, code, msg string) { v.add("warning", stepID, code, msg) }

// steps validates one id namespace: an action's steps (with its loops) or a
// fragment's. prefix qualifies reported step ids ("fragment:x/").
func (v *validator) steps(prefix string, steps []StepDef, loops []LoopDef) {
	ids := map[string]int{}
	var collect func([]StepDef)
	collect = func(ss []StepDef) {
		for _, s := range ss {
			if s.ID != "" {
				ids[s.ID]++
			}
			collect(s.Steps)
		}
	}
	collect(steps)
	var dups []string
	for id, n := range ids {
		if n > 1 {
			dups = append(dups, id)
		}
	}
	sort.Strings(dups)
	for _, id := range dups {
		v.err(prefix+id, "duplicate_id", fmt.Sprintf("step id %q is used %d times", id, ids[id]))
	}

	loopIDs := map[string]bool{}
	for i, l := range loops {
		if l.ID == "" {
			if v.legacy {
				v.warn(prefix, "missing_id", fmt.Sprintf("loop #%d has no id", i+1))
			} else {
				v.err(prefix, "missing_id", fmt.Sprintf("loop #%d has no id", i+1))
			}
		}
		loopIDs[l.ID] = true
		for _, ref := range l.Steps {
			if ids[ref] == 0 {
				v.err(prefix+l.ID, "unknown_step_ref", fmt.Sprintf("loop %q references unknown step %q", l.ID, ref))
			}
		}
	}

	var walk func([]StepDef, string)
	walk = func(ss []StepDef, where string) {
		for i, s := range ss {
			label := prefix + s.ID
			if s.ID == "" {
				label = fmt.Sprintf("%s#%d", prefix+where, i+1)
				if v.legacy {
					v.warn(label, "missing_id", fmt.Sprintf("step #%d (%s) has no id; it runs as %s", i+1, s.Type, syntheticID(i)))
				} else {
					v.err(label, "missing_id", fmt.Sprintf("step #%d (%s) has no id", i+1, s.Type))
				}
			}
			for _, ref := range append(append([]string{}, s.Then...), s.Else...) {
				if ids[ref] == 0 && !loopIDs[ref] {
					v.err(label, "unknown_step_ref", fmt.Sprintf("condition branch references unknown step %q", ref))
				}
			}
			v.step(label, s)
			if len(s.Steps) > 0 {
				walk(s.Steps, s.ID+"/")
			}
		}
	}
	walk(steps, "")
}

// step checks one step's own rules.
func (v *validator) step(id string, s StepDef) {
	if s.Type == "" {
		v.err(id, "unknown_step_type", "step has no type")
		return
	}
	if !v.known[s.Type] {
		v.err(id, "unknown_step_type", fmt.Sprintf("unknown step type %q", s.Type))
		return
	}
	if v.pkg != nil && !stepPermitted(s.Type, v.pkg.PermittedSteps()) {
		v.err(id, "step_not_permitted", fmt.Sprintf("step type %q is not in the manifest's permissions.steps", s.Type))
	}

	switch s.Type {
	case "navigate":
		v.navigate(id, s)
	case "call_fragment":
		v.callFragment(id, s)
	case "page_script":
		v.pageScript(id, s)
	case "call_action":
		v.callAction(id, s)
	case "for_each":
		if s.Items == "" {
			v.err(id, "missing_field", `for_each needs "items"`)
		}
		if len(s.Steps) == 0 {
			v.warn(id, "empty_body", "for_each has no steps")
		}
	}

	if s.ConfigKey != "" && v.pkg != nil && !isTemplate(s.ConfigKey) {
		if _, ok := v.pkg.Selector(s.ConfigKey); !ok && !v.native && !hasSelectorFallback(s) {
			v.err(id, "unknown_selector", fmt.Sprintf("configKey %q is not in selectors.json and the step has no selector, xpath, alternatives or intent", s.ConfigKey))
		}
	}
}

func (v *validator) navigate(id string, s StepDef) {
	u := strings.TrimSpace(s.URL)
	if u == "" {
		v.err(id, "missing_field", `navigate needs "url"`)
		return
	}
	if v.pkg == nil || isTemplate(u) {
		return
	}
	lower := strings.ToLower(u)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return // relative: resolved against the current (checked) page
	}
	if err := URLAllowed(u, v.pkg.Domains()); err != nil {
		v.err(id, "off_domain", err.Error())
	}
}

func (v *validator) callFragment(id string, s StepDef) {
	if s.Fragment == "" {
		v.err(id, "missing_field", `call_fragment needs "fragment"`)
		return
	}
	if v.pkg == nil {
		v.err(id, "missing_fragment", fmt.Sprintf("fragment %q: action has no package", s.Fragment))
		return
	}
	frag, err := v.pkg.Fragment(s.Fragment)
	if err != nil || frag == nil {
		v.err(id, "missing_fragment", fmt.Sprintf("fragment %q not found in the package: %v", s.Fragment, err))
		return
	}
	if v.fragDone[s.Fragment] {
		return // validated once; also stops fragment cycles
	}
	v.fragDone[s.Fragment] = true
	v.steps("fragment:"+s.Fragment+"/", frag.Steps, nil)
}

func (v *validator) pageScript(id string, s StepDef) {
	if s.Script == "" {
		v.err(id, "missing_field", `page_script needs "script"`)
		return
	}
	v.warn(id, "script_used", fmt.Sprintf("page_script %q runs custom JavaScript in the page; prefer a declarative step where one fits", s.Script))
	if v.pkg == nil {
		v.err(id, "missing_script", fmt.Sprintf("script %q: action has no package", s.Script))
		return
	}
	if _, err := v.pkg.Script(s.Script); err != nil {
		v.err(id, "missing_script", fmt.Sprintf("script %q not found in the package: %v", s.Script, err))
	}
}

// stepPermitted reports whether typ matches permissions.steps: exact entries
// or prefix globs ("extract_*"). An empty list permits everything.
func stepPermitted(typ string, permitted []string) bool {
	if len(permitted) == 0 {
		return true
	}
	for _, p := range permitted {
		p = strings.TrimSpace(p)
		if p == typ || p == "*" {
			return true
		}
		if prefix, ok := strings.CutSuffix(p, "*"); ok && strings.HasPrefix(typ, prefix) {
			return true
		}
	}
	return false
}

func hasSelectorFallback(s StepDef) bool {
	return s.Selector != "" || s.XPath != "" || len(s.Alternatives) > 0 || strings.TrimSpace(s.Intent) != ""
}

func isTemplate(s string) bool { return strings.Contains(s, "{{") }

// HasErrors reports whether any issue is an error.
func HasErrors(issues []Issue) bool {
	for _, i := range issues {
		if i.Severity == "error" {
			return true
		}
	}
	return false
}

// isLegacyPackage reports whether actions of p follow the legacy (master)
// rules: no package, or a package migrated from ~/.monoagent/actions
// (id "local-<platform>").
func isLegacyPackage(p PackageContext) bool {
	return p == nil || strings.HasPrefix(strings.ToLower(p.ID()), "local-")
}

func syntheticID(i int) string { return fmt.Sprintf("_s%d", i+1) }

// assignSyntheticIDs returns def with every id-less step (nested bodies
// included) given an id "_s<N>" (N = position in its list), copying only
// what changes; def itself (possibly shared through the loader cache) is
// not modified.
func assignSyntheticIDs(def *ActionDef) *ActionDef {
	steps, changed := withSyntheticIDs(def.Steps)
	if !changed {
		return def
	}
	cp := *def
	cp.Steps = steps
	return &cp
}

func withSyntheticIDs(steps []StepDef) ([]StepDef, bool) {
	changed := false
	var out []StepDef
	for i, s := range steps {
		ns := s
		nested, nch := withSyntheticIDs(s.Steps)
		if nch {
			ns.Steps = nested
		}
		if ns.ID == "" {
			ns.ID = syntheticID(i)
		}
		if nch || s.ID == "" {
			if out == nil {
				out = append([]StepDef(nil), steps...)
			}
			out[i] = ns
			changed = true
		}
	}
	if !changed {
		return steps, false
	}
	return out, true
}
