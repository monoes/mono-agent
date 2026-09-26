package recordanalyze

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/monoes/mono-agent/internal/action"
)

// elementSteps must target an element through a package selector.
var elementSteps = map[string]bool{
	"click": true, "type": true, "select_option": true, "hover": true, "upload": true,
	"extract_text": true, "extract_attribute": true, "extract_multiple": true, "extract_table": true,
}

// AdvancedSteps may run code, reach other packages or move files. An AI
// draft may use them only with --allow-advanced (security review H3).
var AdvancedSteps = map[string]bool{
	"page_script": true, "http_fetch_in_page": true, "call_action": true, "upload": true, "download": true,
}

var sideEffectLevels = map[string]bool{"none": true, "read": true, "write": true, "message": true, "destructive": true}

// FixNames makes every AI-proposed name a valid slug and suffixes
// collisions: the action against the target's actions, a new automation id
// against the registry, fragments against the target's fragments.
func FixNames(out *Output, env *Env) {
	if out.Names == nil {
		out.Names = map[string]string{}
	}
	name := out.Action.ActionType
	if !ValidActionName(name) {
		name = ActionSlug(firstNonEmpty(name, out.Names["action"], goalOf(env)))
	}
	if env.Target != nil {
		name = Unique(name, "_", func(n string) bool { return env.Target.Actions[n] })
	}
	out.Action.ActionType = name

	if env.Target != nil {
		out.Automation.ID = env.Target.Manifest.ID
		if out.Automation.Name == "" {
			out.Automation.Name = env.Target.Manifest.Name
		}
	} else {
		id := out.Automation.ID
		if !ValidAutomationID(id) {
			host := ""
			if env.Analysis != nil && len(env.Analysis.Domains) > 0 {
				host = env.Analysis.Domains[0]
			}
			id = AutomationSlug(firstNonEmpty(id, out.Automation.Name, host))
		}
		out.Automation.ID = Unique(id, "-", env.AutomationTaken)
		if out.Automation.Name == "" {
			out.Automation.Name = out.Automation.ID
		}
	}
	out.Action.Automation = out.Automation.ID

	renamed := map[string]string{}
	used := map[string]bool{}
	for i := range out.Fragments {
		f := &out.Fragments[i]
		n := f.Name
		if !ValidActionName(n) {
			n = ActionSlug(n)
		}
		n = Unique(n, "_", func(c string) bool {
			if used[c] {
				return true
			}
			if env.Target != nil {
				_, ok := env.Target.Fragments[c]
				return ok
			}
			return false
		})
		used[n] = true
		if n != f.Name {
			renamed[f.Name] = n
			f.Name = n
		}
	}
	if len(renamed) > 0 {
		allSteps(out, func(s *action.StepDef) {
			if s.Type == "call_fragment" {
				if n, ok := renamed[s.Fragment]; ok {
					s.Fragment = n
				}
			}
		})
	}
	out.Names["automation"] = out.Automation.ID
	out.Names["action"] = out.Action.ActionType
	if len(out.Fragments) > 0 {
		out.Names["fragment"] = out.Fragments[0].Name
	} else if out.Names["fragment"] == "" || !ValidActionName(out.Names["fragment"]) {
		out.Names["fragment"] = out.Action.ActionType
	}
}

func goalOf(env *Env) string {
	if env.Analysis != nil && env.Analysis.Normalized != nil {
		return env.Analysis.Goal
	}
	return ""
}

// CheckOutput validates the AI output: shape, names, selector references,
// intents, candidates, fragments, scripts, and action.Validate against the
// draft package. The returned strings are fed back in the repair round.
func CheckOutput(out *Output, env *Env) []string {
	var p []string
	add := func(f string, a ...any) { p = append(p, fmt.Sprintf(f, a...)) }
	if out.Action.Description == "" {
		add("action.description is empty")
	}
	if len(out.Action.Steps) == 0 {
		add("action.steps is empty")
	}
	if out.Action.SideEffects != "" && !sideEffectLevels[out.Action.SideEffects] {
		add("action.sideEffects %q must be one of none|read|write|message|destructive", out.Action.SideEffects)
	}
	if out.Action.SideEffects == "" {
		add("action.sideEffects is missing")
	}
	if env.Target == nil && !ValidAutomationID(out.Automation.ID) {
		add("automation.id %q is not a valid id", out.Automation.ID)
	}
	if out.Action.Inputs != nil {
		for _, group := range [][]json.RawMessage{out.Action.Inputs.Required, out.Action.Inputs.Optional} {
			for _, raw := range group {
				var in struct {
					Name string `json:"name"`
					Type string `json:"type"`
				}
				if err := json.Unmarshal(raw, &in); err != nil {
					add("action.inputs entries must be objects {name,type,...}: %s", string(raw))
					continue
				}
				if !ValidInputName(in.Name) {
					add("input name %q is not a valid identifier", in.Name)
				}
			}
		}
	}
	for _, k := range sortedKeys(out.Selectors) {
		e := out.Selectors[k]
		if len(e.Candidates) == 0 {
			add("selectors[%q] has no candidates", k)
		}
		for i, c := range e.Candidates {
			if kinds(c) != 1 {
				add("selectors[%q].candidates[%d] must set exactly one of css, xpath, aria, text", k, i)
			}
		}
	}
	ctx := newDraftContext(out, env, BuildManifest(out, env))
	fragNames := map[string]bool{}
	for _, f := range out.Fragments {
		if !ValidActionName(f.Name) {
			add("fragment name %q is not valid", f.Name)
		}
		if fragNames[f.Name] {
			add("fragment %q is defined twice", f.Name)
		}
		fragNames[f.Name] = true
	}
	allSteps(out, func(s *action.StepDef) {
		if s.ID == "" {
			add("a %s step has no id", s.Type)
		}
		if elementSteps[s.Type] {
			switch {
			case s.ConfigKey == "":
				add("step %q (%s) must reference a selector through configKey", s.ID, s.Type)
			default:
				if _, ok := ctx.Selector(s.ConfigKey); !ok {
					add("step %q: configKey %q is not defined in selectors", s.ID, s.ConfigKey)
				}
			}
			if s.Intent == "" {
				add("step %q (%s) has no intent", s.ID, s.Type)
			}
		}
		if AdvancedSteps[s.Type] && !env.AllowAdvanced {
			add("step %q uses %s, which a recorded draft may not use without --allow-advanced: remove it (express it with declarative steps)", s.ID, s.Type)
		}
		switch s.Type {
		case "call_fragment":
			if _, err := ctx.Fragment(s.Fragment); err != nil {
				add("step %q: %v", s.ID, err)
			}
		case "page_script":
			if _, err := ctx.Script(s.Script); err != nil {
				add("step %q: %v", s.ID, err)
			}
		}
	})
	for _, is := range action.Validate(&out.Action, ctx) {
		if is.Severity == "error" {
			add("%s (step %q): %s", is.Code, is.StepID, is.Message)
		}
	}
	return p
}

func kinds(c action.SelectorCandidate) int {
	n := 0
	for _, set := range []bool{c.CSS != "", c.XPath != "", c.Aria != nil, c.Text != ""} {
		if set {
			n++
		}
	}
	return n
}

// stepTypes lists the step types the output uses (action + fragments).
func stepTypes(out *Output) []string {
	seen := map[string]bool{}
	allSteps(out, func(s *action.StepDef) { seen[s.Type] = true })
	delete(seen, "")
	out2 := make([]string, 0, len(seen))
	for t := range seen {
		out2 = append(out2, t)
	}
	sort.Strings(out2)
	return out2
}
