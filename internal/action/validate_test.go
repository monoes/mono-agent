package action

import (
	"context"
	"encoding/json"
	"io/fs"
	"path"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/data"
)

// seedPkg is a PackageContext over one embedded built-in package
// (manifest, fragments/, scripts/, selectors.json), for validation tests.
func seedPkg(t *testing.T, id string) *fakePkg {
	t.Helper()
	root := "automations/" + id
	var m struct {
		Site struct {
			StartURL string   `json:"startUrl"`
			Domains  []string `json:"domains"`
		} `json:"site"`
		Permissions struct {
			Steps []string `json:"steps"`
		} `json:"permissions"`
		Requires struct {
			Native string `json:"native"`
		} `json:"requires"`
	}
	raw, err := data.AutomationsFS.ReadFile(root + "/automation.json")
	if err != nil {
		t.Fatalf("%s: %v", id, err)
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("%s manifest: %v", id, err)
	}
	p := &fakePkg{id: id, startURL: m.Site.StartURL, domains: m.Site.Domains, permitted: m.Permissions.Steps,
		native: m.Requires.Native, fragments: map[string]*FragmentDef{}, scripts: map[string]string{},
		selectors: map[string]*SelectorEntry{}}
	frags, _ := fs.Glob(data.AutomationsFS, root+"/fragments/*.json")
	for _, f := range frags {
		b, _ := data.AutomationsFS.ReadFile(f)
		var fd FragmentDef
		if err := json.Unmarshal(b, &fd); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		p.fragments[strings.TrimSuffix(path.Base(f), ".json")] = &fd
	}
	scripts, _ := fs.Glob(data.AutomationsFS, root+"/scripts/*")
	for _, f := range scripts {
		b, _ := data.AutomationsFS.ReadFile(f)
		p.scripts[path.Base(f)] = string(b)
	}
	if b, err := data.AutomationsFS.ReadFile(root + "/selectors.json"); err == nil {
		if err := json.Unmarshal(b, &p.selectors); err != nil {
			t.Fatalf("%s selectors.json: %v", id, err)
		}
	}
	return p
}

// Every shipped built-in action validates with zero errors against its own
// package; native-backed (legacy) packages also validate with no package.
func TestBuiltinActionsValidate(t *testing.T) {
	files, err := fs.Glob(data.AutomationsFS, "automations/*/actions/*.json")
	if err != nil || len(files) == 0 {
		t.Fatalf("glob: %v (%d files)", err, len(files))
	}
	pkgs := map[string]*fakePkg{}
	for _, f := range files {
		raw, err := data.AutomationsFS.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		def, err := ParseActionDef(raw)
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		id := path.Base(path.Dir(path.Dir(f)))
		if pkgs[id] == nil {
			pkgs[id] = seedPkg(t, id)
		}
		for _, is := range Validate(def, pkgs[id]) {
			if is.Severity == "error" {
				t.Errorf("%s: %s %s: %s", f, is.StepID, is.Code, is.Message)
			}
		}
		if pkgs[id].native != "" && HasErrors(Validate(def, nil)) {
			t.Errorf("%s: legacy action has errors without a package: %v", f, codes(Validate(def, nil), "error"))
		}
	}
}

func TestKnownStepTypes(t *testing.T) {
	known := map[string]bool{}
	for _, s := range KnownStepTypes() {
		known[s] = true
	}
	for _, s := range []string{"navigate", "click", "type", "call_bot_method", "condition", "set_variable"} {
		if !known[s] {
			t.Errorf("KnownStepTypes lacks %q", s)
		}
	}
}

func codes(issues []Issue, sev string) []string {
	var out []string
	for _, i := range issues {
		if i.Severity == sev {
			out = append(out, i.Code+"@"+i.StepID)
		}
	}
	return out
}

func hasCode(issues []Issue, code string) bool {
	for _, i := range issues {
		if i.Code == code {
			return true
		}
	}
	return false
}

func TestValidateRules(t *testing.T) {
	pkg := &fakePkg{
		id:        "acme",
		domains:   []string{"acme.com"},
		permitted: []string{"navigate", "click", "extract_*", "call_fragment", "page_script", "condition", "for_each", "log"},
		fragments: map[string]*FragmentDef{"banner": {Name: "banner", Steps: []StepDef{
			{ID: "x", Type: "click", Selector: ".close"},
			{ID: "x", Type: "click", Selector: ".close2"},
		}}},
		selectors: map[string]*SelectorEntry{"save": {Candidates: []SelectorCandidate{{CSS: "#save"}}}},
		scripts:   map[string]string{"ok.js": "return 1"},
	}
	cases := []struct {
		name string
		step StepDef
		code string
	}{
		{"unknown type", StepDef{ID: "a", Type: "teleport"}, "unknown_step_type"},
		{"not permitted", StepDef{ID: "a", Type: "type", Selector: "#q"}, "step_not_permitted"},
		{"missing fragment", StepDef{ID: "a", Type: "call_fragment", Fragment: "nope"}, "missing_fragment"},
		{"fragment dup ids", StepDef{ID: "a", Type: "call_fragment", Fragment: "banner"}, "duplicate_id"},
		{"missing script", StepDef{ID: "a", Type: "page_script", Script: "nope.js"}, "missing_script"},
		{"unknown selector", StepDef{ID: "a", Type: "click", ConfigKey: "nope"}, "unknown_selector"},
		{"off domain navigate", StepDef{ID: "a", Type: "navigate", URL: "https://evil.com/"}, "off_domain"},
		{"missing id", StepDef{Type: "click", Selector: "#a"}, "missing_id"},
		{"unknown branch ref", StepDef{ID: "a", Type: "condition", Condition: "x == 1", Then: []string{"ghost"}}, "unknown_step_ref"},
		{"nested unknown", StepDef{ID: "a", Type: "for_each", Items: "{{xs}}", Steps: []StepDef{{ID: "b", Type: "teleport"}}}, "unknown_step_type"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			def := &ActionDef{ActionType: "t", SideEffects: "read", Steps: []StepDef{c.step}}
			issues := Validate(def, pkg)
			if !hasCode(issues, c.code) || !HasErrors(issues) {
				t.Fatalf("want error %s, got %v / %v", c.code, codes(issues, "error"), codes(issues, "warning"))
			}
		})
	}

	ok := &ActionDef{ActionType: "t", SideEffects: "write", Steps: []StepDef{
		{ID: "go", Type: "navigate", URL: "https://acme.com/new"},
		{ID: "b", Type: "call_fragment", Fragment: "banner_ok"},
		{ID: "s", Type: "click", ConfigKey: "save", SideEffect: true},
		{ID: "k", Type: "click", ConfigKey: "unknown_but_intent", Intent: "the Save button"},
		{ID: "js", Type: "page_script", Script: "ok.js"},
		{ID: "e", Type: "extract_text", Selector: "h1"},
	}}
	pkg.fragments["banner_ok"] = &FragmentDef{Name: "banner_ok", Steps: []StepDef{{ID: "x", Type: "click", Selector: ".close"}}}
	issues := Validate(ok, pkg)
	if HasErrors(issues) {
		t.Fatalf("valid action has errors: %v", codes(issues, "error"))
	}
	if !hasCode(issues, "script_used") {
		t.Error("page_script should warn script_used")
	}

	vis := &ActionDef{ActionType: "t", SideEffects: "read", Visibility: []string{"search_may_be_saved", "seen_by_everyone"},
		Steps: []StepDef{{ID: "a", Type: "log"}}}
	if is := Validate(vis, nil); !hasCode(is, "invalid_visibility") || !HasErrors(is) {
		t.Errorf("unknown visibility: want error invalid_visibility, got %v", codes(is, "error"))
	} else if n := len(codes(is, "error")); n != 1 {
		t.Errorf("only the unknown value is an error, got %v", codes(is, "error"))
	}

	noSE := &ActionDef{ActionType: "t", Steps: []StepDef{{ID: "a", Type: "log"}}}
	if is := Validate(noSE, nil); HasErrors(is) || !hasCode(is, "no_side_effects") {
		t.Errorf("missing sideEffects: want warning only, got %v", is)
	}
	dup := &ActionDef{ActionType: "t", SideEffects: "none", Steps: []StepDef{{ID: "a", Type: "log"}, {ID: "a", Type: "log"}},
		Loops: []LoopDef{{ID: "l", Steps: []string{"zz"}}}}
	is := Validate(dup, nil)
	if !hasCode(is, "duplicate_id") || !hasCode(is, "unknown_step_ref") {
		t.Errorf("dup/loop ref: got %v", codes(is, "error"))
	}
}

func TestExecuteRefusesInvalidAndUnknownAtRuntime(t *testing.T) {
	ae := newPkgExecutor(nil, nil)
	def := &ActionDef{ActionType: "t", SideEffects: "none", Steps: []StepDef{{ID: "a", Type: "teleport"}}}
	_, err := ae.ExecuteDef(&StorageAction{ID: "a", TargetPlatform: "p", Type: "t"}, def)
	var verr *ValidationError
	if err == nil || !strings.Contains(err.Error(), "unknown_step_type") {
		t.Fatalf("err = %v", err)
	}
	if !asValidation(err, &verr) {
		t.Fatalf("err %T is not *ValidationError", err)
	}

	// RunSteps bypasses Validate: an unknown type is still an error, not a skip.
	ae2 := newPkgExecutor(nil, nil)
	if err := ae2.RunSteps(context.Background(), []StepDef{{ID: "a", Type: "teleport"}, {ID: "b", Type: "log"}}); err == nil {
		t.Fatal("unknown step type at run time must fail")
	}
	if ae2.execCtx.GetStepResult("b") != nil {
		t.Fatal("run continued past an unknown step type")
	}
}

func asValidation(err error, target **ValidationError) bool {
	v, ok := err.(*ValidationError)
	if ok {
		*target = v
	}
	return ok
}

// Master ran legacy actions without step ids; they still validate (warning)
// and run with synthetic ids. Packaged actions still need ids.
func TestLegacyStepsWithoutIDs(t *testing.T) {
	def := &ActionDef{ActionType: "bar", Platform: "foo", Steps: []StepDef{
		{Type: "set_variable", Variable: "a", Value: 1},
		{Type: "for_each", Items: "{{xs}}", Steps: []StepDef{{Type: "log", Text: "x"}}},
	}}
	for _, pkg := range []PackageContext{nil, &fakePkg{id: "local-foo"}} {
		is := Validate(def, pkg)
		if HasErrors(is) || !hasCode(is, "missing_id") {
			t.Fatalf("legacy pkg=%v: %+v", pkg, is)
		}
	}
	if !HasErrors(Validate(def, &fakePkg{id: "acme"})) {
		t.Fatal("packaged action without ids must fail")
	}

	ae := newPkgExecutor(nil, nil)
	if _, err := ae.ExecuteDef(&StorageAction{ID: "r", TargetPlatform: "foo", Type: "bar"}, def); err != nil {
		t.Fatalf("legacy run: %v", err)
	}
	if ae.execCtx.GetStepResult("_s1") == nil {
		t.Error("synthetic id _s1 not assigned")
	}
	if def.Steps[0].ID != "" {
		t.Error("the shared definition was modified")
	}
}
