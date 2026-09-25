package action

import (
	"errors"
	"strings"
	"testing"
)

// trustPkg adds the optional trust interfaces to fakePkg and resolves
// call_action refs through a small registry.
type trustPkg struct {
	fakePkg
	trust   string
	calls   []string
	scripts *bool
	live    *bool
	reg     map[string]*trustPkg
	actions map[string]*ActionDef
}

func (p *trustPkg) Trust() string         { return p.trust }
func (p *trustPkg) CallActions() []string { return p.calls }
func (p *trustPkg) ScriptsAllowed() bool  { return p.scripts == nil || *p.scripts }
func (p *trustPkg) LiveRunConfirmed() bool {
	return p.live == nil || *p.live
}
func (p *trustPkg) ResolveAction(ref string) (*ActionDef, PackageContext, error) {
	pkgID, name := p.id, ref
	if i := strings.Index(ref, "."); i >= 0 {
		pkgID, name = ref[:i], ref[i+1:]
	}
	t := p.reg[pkgID]
	if t == nil || t.actions[name] == nil {
		return nil, nil, errors.New("not installed")
	}
	return t.actions[name], t, nil
}

func trustWorld() (imported, local *trustPkg) {
	reg := map[string]*trustPkg{}
	mk := func(id, trust string, domains []string, native string) *trustPkg {
		p := &trustPkg{fakePkg: fakePkg{id: id, domains: domains, native: native}, trust: trust, reg: reg,
			actions: map[string]*ActionDef{"act": {ActionType: "act", SideEffects: "read"}}}
		reg[id] = p
		return p
	}
	imported = mk("imp", "imported", []string{"imp.example"}, "")
	local = mk("loc", "local", []string{"loc.example"}, "")
	mk("instagram", "builtin", nil, "instagram")
	mk("social", "local", []string{"*.tiktok.com"}, "")
	mk("helper", "local", []string{"help.example"}, "")
	return
}

func TestCheckCallAction(t *testing.T) {
	imp, loc := trustWorld()
	imp.calls = []string{"helper.act", "instagram.act", "social.act"}
	loc.calls = []string{"instagram.act"}

	code := func(err error) string {
		var cae *CallActionError
		if errors.As(err, &cae) {
			return cae.Code
		}
		if errors.Is(err, errUnresolvedAction) {
			return "unresolved"
		}
		return ""
	}
	cases := []struct {
		caller *trustPkg
		ref    string
		want   string
	}{
		{imp, "act", ""},                                       // own package
		{imp, "helper.act", ""},                                // listed, local target
		{imp, "{{target}}", "call_action_template"},            // templates refused
		{imp, "loc.act", "call_action_not_allowed"},            // not listed
		{imp, "instagram.act", "call_action_forbidden_target"}, // builtin target
		{imp, "social.act", "call_action_forbidden_target"},    // social domain
		{loc, "instagram.act", ""},                             // local may call builtin when listed
		{imp, "helper.nope", "unresolved"},
	}
	for _, c := range cases {
		_, _, err := CheckCallAction(c.caller, c.ref)
		if got := code(err); got != c.want || (c.want == "" && err != nil) {
			t.Errorf("%s → %q: err=%v (code %q), want %q", c.caller.id, c.ref, err, got, c.want)
		}
	}

	// Validate surfaces the same codes; unresolved is only a warning.
	def := &ActionDef{ActionType: "t", SideEffects: "read", Steps: []StepDef{
		{ID: "a", Type: "call_action", Action: "instagram.act"},
		{ID: "b", Type: "call_action", Action: "helper.nope"},
	}}
	is := Validate(def, imp)
	if !hasCode(is, "call_action_forbidden_target") || !hasCode(is, "unresolved_action") {
		t.Fatalf("issues: %+v", is)
	}
	if is := Validate(&ActionDef{ActionType: "t", SideEffects: "read", Steps: []StepDef{{ID: "a", Type: "call_action", Action: "{{x}}"}}}, nil); !hasCode(is, "call_action_template") {
		t.Fatalf("template without package: %+v", is)
	}
}

func TestUnknownTrustIsImported(t *testing.T) {
	if got := PackageTrust(&fakePkg{id: "x"}); got != "imported" {
		t.Fatalf("PackageTrust = %q", got)
	}
}

func TestValidateUnflaggedSideEffect(t *testing.T) {
	def := &ActionDef{ActionType: "t", SideEffects: "write", Steps: []StepDef{{ID: "c", Type: "click", Selector: "#save"}}}
	if !hasCode(Validate(def, nil), "unflagged_side_effect") {
		t.Fatal("write action with no flagged step must be an error")
	}
	def.Steps[0].SideEffect = true
	if HasErrors(Validate(def, nil)) {
		t.Fatal("flagged step should satisfy the rule")
	}
	// A flag inside a fragment counts.
	pkg := &fakePkg{id: "p", fragments: map[string]*FragmentDef{"save": {Steps: []StepDef{{ID: "s", Type: "click", Selector: "#s", SideEffect: true}}}}}
	frag := &ActionDef{ActionType: "t", SideEffects: "message", Steps: []StepDef{{ID: "f", Type: "call_fragment", Fragment: "save"}}}
	if HasErrors(Validate(frag, pkg)) {
		t.Fatalf("flag in fragment: %+v", Validate(frag, pkg))
	}
	read := &ActionDef{ActionType: "t", SideEffects: "read", Steps: []StepDef{{ID: "c", Type: "click", Selector: "#a"}}}
	if HasErrors(Validate(read, nil)) {
		t.Fatal("read action needs no flag")
	}
}

func TestScriptsAllowed(t *testing.T) {
	no := false
	imp, _ := trustWorld()
	imp.scripts = &no
	if newPkgExecutor(nil, imp).scriptsAllowed() {
		t.Error("ScriptsAllowed()=false must refuse")
	}
	if !newPkgExecutor(nil, &fakePkg{id: "x"}).scriptsAllowed() {
		t.Error("a package without the gate allows scripts")
	}
}

func TestLiveRunGate(t *testing.T) {
	no := false
	imp, _ := trustWorld()
	imp.live = &no
	def := &ActionDef{ActionType: "t", SideEffects: "write", Steps: []StepDef{
		{ID: "s", Type: "set_variable", Variable: "x", Value: 1, SideEffect: true}}}
	act := &StorageAction{ID: "a", TargetPlatform: "imp", Type: "t"}
	if _, err := newPkgExecutor(nil, imp).ExecuteDef(act, def); err == nil || !strings.Contains(err.Error(), "--live") {
		t.Fatalf("unconfirmed live run: %v", err)
	}
	ae := newPkgExecutor(nil, imp)
	ae.SetSafeMode(true)
	if _, err := ae.ExecuteDef(act, def); err != nil {
		t.Fatalf("safe mode must not need confirmation: %v", err)
	}
	def.SideEffects = "read"
	def.Steps[0].SideEffect = false
	if _, err := newPkgExecutor(nil, imp).ExecuteDef(act, def); err != nil {
		t.Fatalf("read action: %v", err)
	}
}
