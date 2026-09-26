package recordanalyze

import (
	"fmt"
	"strings"

	"github.com/monoes/mono-agent/internal/action"
	"github.com/monoes/mono-agent/internal/automation"
)

var effectRank = map[string]int{"": 0, "none": 0, "read": 1, "write": 2, "message": 3, "destructive": 4}

// EnforceSideEffects makes the deterministic detector authoritative for
// side effects (security review H3): every AI step that targets a detected
// side-effecting element gets sideEffect:true back if the AI dropped it,
// a detected side effect with no matching step is an error, and the
// action's sideEffects is at least "write" when any was detected.
func EnforceSideEffects(out *Output, env *Env) []automation.IssueJSON {
	if env.Analysis == nil {
		return nil
	}
	file := "actions/" + out.Action.ActionType + ".json"
	var issues []automation.IssueJSON
	ctx := newDraftContext(out, env, &automation.Manifest{})
	detected := 0
	for _, d := range env.Analysis.Steps {
		if !d.SideEffect || d.Login {
			continue
		}
		detected++
		matched := false
		allSteps(out, func(s *action.StepDef) {
			if !stepTargets(s, &d, ctx) {
				return
			}
			matched = true
			if !s.SideEffect {
				s.SideEffect = true
				issues = append(issues, automation.IssueJSON{File: file, Severity: "warning", StepID: s.ID, Code: "side_effect_restored",
					Message: "the recording shows this step changes the site; sideEffect restored"})
			}
		})
		if !matched {
			issues = append(issues, automation.IssueJSON{File: file, Severity: "error", Code: "side_effect_unmatched",
				Message: fmt.Sprintf("recorded side effect %s (%s) has no matching step marked sideEffect", d.EventID, describeTarget(&d))})
		}
	}
	if detected > 0 && effectRank[out.Action.SideEffects] < effectRank["write"] {
		out.Action.SideEffects = "write"
	}
	return issues
}

func describeTarget(d *Step) string {
	if d.Target == nil {
		return d.Kind
	}
	return strings.TrimSpace(d.Kind + " " + firstNonEmpty(d.Target.Text, d.Target.AriaName, d.Target.Name, d.Target.ID))
}

// stepTargets reports whether AI step s acts on the element of detected step d.
func stepTargets(s *action.StepDef, d *Step, ctx *draftContext) bool {
	switch d.Kind {
	case KindPressKey:
		return s.Type == "press_key" && strings.EqualFold(s.Key, d.Key)
	case KindClick, KindSubmit:
		if s.Type != "click" && s.Type != "press_key" {
			return false
		}
	default:
		return false
	}
	var cands []action.SelectorCandidate
	if e, ok := ctx.Selector(s.ConfigKey); ok {
		cands = e.Candidates
	}
	if s.Selector != "" {
		cands = append(cands, action.SelectorCandidate{CSS: s.Selector})
	}
	fp := d.Target
	for _, c := range cands {
		for _, dc := range d.Candidates {
			if (c.CSS != "" && c.CSS == dc.CSS) || (c.XPath != "" && c.XPath == dc.XPath) || (c.Text != "" && c.Text == dc.Text) ||
				(c.Aria != nil && dc.Aria != nil && *c.Aria == *dc.Aria) {
				return true
			}
		}
		if fp == nil {
			continue
		}
		switch {
		case c.CSS != "" && fp.TestID != "" && strings.Contains(c.CSS, fp.TestID):
			return true
		case c.CSS != "" && fp.ID != "" && strings.Contains(c.CSS, "#"+fp.ID):
			return true
		case c.Aria != nil && c.Aria.Name != "" && (c.Aria.Name == fp.AriaName || c.Aria.Name == fp.Text):
			return true
		case c.Text != "" && c.Text == strings.TrimSpace(fp.Text):
			return true
		}
	}
	return false
}
