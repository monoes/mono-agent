package automation

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/monoes/mono-agent/internal/action"
)

// Validate validates a package: manifest, every action (action.Validate),
// fragments, selectors, scripts declared ↔ present, icon, and the
// imported-package rules. Issues are sorted errors first.
func Validate(p *Package) []IssueJSON {
	if p == nil {
		return []IssueJSON{{Severity: "error", Code: "no_package", Message: "no package"}}
	}
	m := p.Manifest
	out := validateManifest(m, p.Source)
	if is := engineIssue(m); is != nil {
		out = append(out, *is)
	}
	add := func(file, sev, code, format string, a ...any) {
		out = append(out, IssueJSON{File: file, Severity: sev, Code: code, Message: fmt.Sprintf(format, a...)})
	}
	ctx := p.Context()

	// Actions: listed ↔ present, each validated by the engine.
	listed := map[string]bool{}
	for _, name := range m.Actions {
		listed[name] = true
		if !safeName(name) {
			continue
		}
		file := "actions/" + name + ".json"
		def, err := p.Action(name)
		if err != nil {
			if _, statErr := fs.Stat(p.FS, file); statErr != nil {
				add(file, "error", "missing_action_file", "action %q is listed in the manifest but %s is missing", name, file)
			} else {
				add(file, "error", "bad_action_json", "%v", err)
			}
			continue
		}
		if def.ActionType != "" && !strings.EqualFold(def.ActionType, name) {
			add(file, "warning", "action_type_mismatch", "actionType %q differs from file name %q", def.ActionType, name)
		}
		for _, is := range action.Validate(def, ctx) {
			out = append(out, IssueJSON{File: file, Severity: is.Severity, StepID: is.StepID, Code: is.Code, Message: is.Message})
		}
	}
	for _, name := range p.ActionFiles() {
		if !listed[name] {
			add("actions/"+name+".json", "warning", "unlisted_action", "action file is not listed in the manifest actions")
		}
	}

	// Fragments parse and their steps validate like an action's.
	for _, name := range p.FragmentNames() {
		file := "fragments/" + name + ".json"
		frag, err := p.Fragment(name)
		if err != nil {
			add(file, "error", "bad_fragment_json", "%v", err)
			continue
		}
		def := &action.ActionDef{ActionType: name, Automation: m.ID, Inputs: frag.Inputs, Steps: frag.Steps, SideEffects: "none"}
		for _, is := range action.Validate(def, ctx) {
			out = append(out, IssueJSON{File: file, Severity: is.Severity, StepID: is.StepID, Code: is.Code, Message: is.Message})
		}
	}
	for _, name := range m.Requires.Fragments {
		if _, err := fs.Stat(p.FS, "fragments/"+name+".json"); err != nil {
			add(ManifestFile, "error", "missing_fragment", "requires.fragments names %q but fragments/%s.json is missing", name, name)
		}
	}

	out = append(out, validateSelectors(p)...)

	// Scripts: declared ↔ present; any script is surfaced as a warning.
	present := map[string]bool{}
	for _, s := range p.ScriptFiles() {
		present[s] = true
	}
	declared := map[string]bool{}
	for _, s := range m.Permissions.Scripts {
		declared[s] = true
		if !present[s] {
			add(ManifestFile, "error", "missing_script", "permissions.scripts declares %q but scripts/%s is missing", s, s)
		}
	}
	for _, s := range p.ScriptFiles() {
		if !declared[s] {
			add("scripts/"+s, "error", "undeclared_script", "script is not declared in permissions.scripts")
		}
	}
	if len(present) > 0 {
		add(ManifestFile, "warning", "contains_scripts", "package contains %d page script(s); review scripts/ before trusting it", len(present))
	}

	if m.Icon != "" {
		if !safePath(m.Icon) {
			add(ManifestFile, "error", "bad_icon", "icon %q is not a package-relative path", m.Icon)
		} else if _, err := fs.Stat(p.FS, m.Icon); err != nil {
			add(ManifestFile, "error", "missing_icon", "icon %q not found in the package", m.Icon)
		}
	}

	sortIssues(out)
	return out
}

func validateSelectors(p *Package) []IssueJSON {
	b, err := fs.ReadFile(p.FS, "selectors.json")
	if err != nil {
		return nil
	}
	var out []IssueJSON
	var sel map[string]action.SelectorEntry
	if err := json.Unmarshal(b, &sel); err != nil {
		return []IssueJSON{{File: "selectors.json", Severity: "error", Code: "bad_selectors_json", Message: err.Error()}}
	}
	keys := make([]string, 0, len(sel))
	for k := range sel {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		e := sel[k]
		if len(e.Candidates) == 0 {
			out = append(out, IssueJSON{File: "selectors.json", Severity: "error", Code: "empty_selector",
				Message: fmt.Sprintf("selector %q has no candidates", k)})
		}
		for i, c := range e.Candidates {
			n := 0
			for _, set := range []bool{c.CSS != "", c.XPath != "", c.Aria != nil, c.Text != ""} {
				if set {
					n++
				}
			}
			if n != 1 {
				out = append(out, IssueJSON{File: "selectors.json", Severity: "error", Code: "bad_selector_candidate",
					Message: fmt.Sprintf("selector %q candidate %d must set exactly one of css, xpath, aria, text", k, i)})
			}
		}
	}
	return out
}

// safePath reports whether p is a clean relative path inside the package.
func safePath(p string) bool {
	return fs.ValidPath(p) && p != "." && !strings.Contains(p, `\`)
}

func sortIssues(is []IssueJSON) {
	rank := func(s string) int {
		if s == "error" {
			return 0
		}
		return 1
	}
	sort.SliceStable(is, func(i, j int) bool { return rank(is[i].Severity) < rank(is[j].Severity) })
}

// HasErrors reports whether any issue is an error.
func HasErrors(is []IssueJSON) bool {
	for _, i := range is {
		if i.Severity == "error" {
			return true
		}
	}
	return false
}

// firstError returns the first error issue's text.
func firstError(is []IssueJSON) string {
	for _, i := range is {
		if i.Severity == "error" {
			if i.File != "" {
				return i.File + ": " + i.Message
			}
			return i.Message
		}
	}
	return ""
}
