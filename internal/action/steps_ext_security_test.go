package action

import (
	"fmt"
	"strings"
	"testing"
)

// Contract §8 (security hardening) checks owned by the extended steps.

func TestScriptCapabilitiesRefusedWithoutTrust(t *testing.T) {
	page := &extPage{eval: func(string) (interface{}, error) {
		t.Fatal("nothing may run in the page for an untrusted package")
		return nil, nil
	}}
	ae := newExtExecutor(t, page)
	ae.SetPackage(&extPkg{id: "shady", noScripts: true, scripts: map[string]string{"x.js": "return 1"}})
	wantFail(t, runExt(t, ae, StepDef{ID: "p", Type: "page_script", Script: "x.js"}), "automation trust shady --scripts")
	wantFail(t, runExt(t, ae, StepDef{ID: "f", Type: "http_fetch_in_page", URL: "https://x.test/"}), "scripts are not allowed")
}

func TestSafeModeStopsSideEffectStepTypes(t *testing.T) {
	cases := []struct {
		name string
		step StepDef
		prep func(ae *ActionExecutor)
	}{
		{"page_script", StepDef{ID: "s", Type: "page_script", Script: "x.js"}, nil},
		{"download", StepDef{ID: "s", Type: "download", URL: "https://site.test/f"}, func(ae *ActionExecutor) { ae.SetDownloadsAllowed(true) }},
		{"post fetch", StepDef{ID: "s", Type: "http_fetch_in_page", URL: "https://site.test/api", Method: "POST"}, nil},
		{"writing call_action", StepDef{ID: "s", Type: "call_action", Action: "send"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			page := &extPage{eval: func(string) (interface{}, error) {
				t.Fatal("safe mode must stop before the page is touched")
				return nil, nil
			}}
			ae := newExtExecutor(t, page)
			ae.SetPackage(&extPkg{id: "site", scripts: map[string]string{"x.js": "return 1"},
				actions: map[string]*ActionDef{"send": {ActionType: "send", SideEffects: "message",
					Steps: []StepDef{setVarStep("ran", "ran", "yes")}}}})
			if c.prep != nil {
				c.prep(ae)
			}
			ae.SetSafeMode(true)
			res := runExt(t, ae, c.step)
			if res.Success || !res.Abort {
				t.Fatalf("want an aborting stop, got %+v", res)
			}
			if s := ae.SafeStopped(); s == nil || s.StepID != "s" || s.Type != c.step.Type {
				t.Fatalf("safe stop = %+v", s)
			}
			if getVar(ae, "ran") != nil {
				t.Fatal("the called action ran")
			}
		})
	}
}

func TestSafeModeLetsReadsThrough(t *testing.T) {
	page := &extPage{eval: func(string) (interface{}, error) { return jsOut(map[string]interface{}{"status": 200}), nil }}
	ae := newExtExecutor(t, page)
	ae.SetPackage(&extPkg{id: "site", actions: map[string]*ActionDef{
		"look": {ActionType: "look", SideEffects: "read", Steps: []StepDef{setVarStep("ran", "ran", "yes")}},
	}})
	ae.SetSafeMode(true)
	wantOK(t, runExt(t, ae, StepDef{ID: "g", Type: "http_fetch_in_page", URL: "https://site.test/api"}))
	wantOK(t, runExt(t, ae, StepDef{ID: "c", Type: "call_action", Action: "look"}))
	if ae.SafeStopped() != nil || getVar(ae, "ran") != "yes" {
		t.Fatalf("reads must run in safe mode: stop=%+v ran=%v", ae.SafeStopped(), getVar(ae, "ran"))
	}
}

func TestFetchRedirectPolicy(t *testing.T) {
	var js string
	page := &extPage{eval: func(s string) (interface{}, error) {
		js = s
		return jsOut(map[string]interface{}{"status": 200}), nil
	}}
	ae := newExtExecutor(t, page)
	ae.SetPackage(&extPkg{id: "site", domains: []string{"site.test"}})

	wantOK(t, runExt(t, ae, StepDef{ID: "f", Type: "http_fetch_in_page", URL: "https://site.test/a"}))
	if !strings.Contains(js, `"redirect":"follow"`) {
		t.Fatalf("a bare GET may follow redirects: %s", js)
	}
	for _, step := range []StepDef{
		{ID: "f", Type: "http_fetch_in_page", URL: "https://site.test/a", Method: "POST", Inputs: map[string]interface{}{"body": "x"}},
		{ID: "f", Type: "http_fetch_in_page", URL: "https://site.test/a", Inputs: map[string]interface{}{"headers": map[string]interface{}{"X-Token": "t"}}},
	} {
		wantOK(t, runExt(t, ae, step))
		if !strings.Contains(js, `"redirect":"manual"`) || !strings.Contains(js, "opaqueredirect") {
			t.Fatalf("requests with a body or headers must not follow redirects: %s", js)
		}
	}

	page.eval = func(string) (interface{}, error) {
		return jsOut(map[string]interface{}{"__error": "the server redirected; redirects are not followed"}), nil
	}
	wantFail(t, runExt(t, ae, StepDef{ID: "f", Type: "http_fetch_in_page", URL: "https://site.test/a", Method: "POST"}), "redirected")
}

func TestForEachDefaultCap(t *testing.T) {
	ae := newExtExecutor(t, &extPage{})
	big := make([]interface{}, maxForEachItems+5)
	ae.SetVariable("big", big)
	res := runExt(t, ae, StepDef{ID: "fe", Type: "for_each", Items: "big"})
	wantOK(t, res)
	if res.Data != maxForEachItems {
		t.Fatalf("processed %v, want the %d cap", res.Data, maxForEachItems)
	}
	res = runExt(t, ae, StepDef{ID: "fe", Type: "for_each", Items: "big", BatchSize: 3})
	wantOK(t, res)
	if res.Data != 3 {
		t.Fatalf("batchSize cap: processed %v", res.Data)
	}
}

// extTrustPkg adds the §8 trust interfaces to extPkg.
type extTrustPkg struct {
	*extPkg
	trust     string
	calls     []string
	tier      string
	confirmed bool
	others    map[string]PackageContext
}

func (p *extTrustPkg) Trust() string          { return p.trust }
func (p *extTrustPkg) CallActions() []string  { return p.calls }
func (p *extTrustPkg) Tier() string           { return p.tier }
func (p *extTrustPkg) LiveRunConfirmed() bool { return p.confirmed }
func (p *extTrustPkg) ResolveAction(ref string) (*ActionDef, PackageContext, error) {
	if auto, name, ok := strings.Cut(ref, "."); ok && auto != p.id {
		o, found := p.others[auto]
		if !found {
			return nil, nil, fmt.Errorf("automation %q not installed", auto)
		}
		return o.ResolveAction(name)
	}
	_, name, ok := strings.Cut(ref, ".")
	if !ok {
		name = ref
	}
	if a, ok := p.actions[name]; ok {
		return a, p, nil
	}
	return nil, nil, fmt.Errorf("action %q not found", ref)
}

func TestCallActionPolicy(t *testing.T) {
	writeStep := setVarStep("ran", "ran", "yes")
	writeStep.SideEffect = true
	writeDef := &ActionDef{ActionType: "post", SideEffects: "write", Steps: []StepDef{writeStep}}
	readDef := &ActionDef{ActionType: "list", SideEffects: "read", Steps: []StepDef{setVarStep("ran", "ran", "yes")}}
	newTarget := func(trust, tier string, confirmed bool) *extTrustPkg {
		return &extTrustPkg{extPkg: &extPkg{id: "target", actions: map[string]*ActionDef{"post": writeDef, "list": readDef}}, trust: trust, tier: tier, confirmed: confirmed}
	}
	cases := []struct {
		name    string
		caller  string
		calls   []string
		target  *extTrustPkg
		ref     string
		wantErr string
	}{
		{"declared call from local", "local", []string{"target.list"}, newTarget("local", "", false), "target.list", ""},
		{"undeclared cross-package call", "local", nil, newTarget("local", "", false), "target.list", "permissions.callActions"},
		{"template ref", "local", []string{"target.list"}, newTarget("local", "", false), "{{which}}", "must be literal"},
		{"imported caller to builtin", "imported", []string{"target.list"}, newTarget("builtin", "", false), "target.list", "may not call"},
		{"recorded caller to social tier", "recorded", []string{"target.list"}, newTarget("local", "social", false), "target.list", "may not call"},
		{"imported target write unconfirmed", "local", []string{"target.post"}, newTarget("imported", "", false), "target.post", "confirmed for live runs"},
		{"imported target write confirmed", "local", []string{"target.post"}, newTarget("imported", "", true), "target.post", ""},
		{"imported target read needs no confirmation", "local", []string{"target.list"}, newTarget("imported", "", false), "target.list", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			caller := &extTrustPkg{extPkg: &extPkg{id: "caller"}, trust: c.caller, calls: c.calls}
			caller.others = map[string]PackageContext{"target": c.target}
			ae := newExtExecutor(t, &extPage{})
			ae.SetPackage(caller)
			res := runExt(t, ae, StepDef{ID: "c", Type: "call_action", Action: c.ref})
			if c.wantErr == "" {
				wantOK(t, res)
				if getVar(ae, "ran") != "yes" {
					t.Fatal("target did not run")
				}
				return
			}
			wantFail(t, res, c.wantErr)
			if getVar(ae, "ran") != nil {
				t.Fatal("refused target ran")
			}
		})
	}
}

func TestCallActionRescopesSecrets(t *testing.T) {
	target := &extTrustPkg{extPkg: &extPkg{id: "target", actions: map[string]*ActionDef{
		"use": {ActionType: "use", SideEffects: "read", Steps: []StepDef{setVarStep("inner", "inner", "{{secret:token}}")}},
	}}, trust: "local"}
	caller := &extTrustPkg{extPkg: &extPkg{id: "caller"}, trust: "local", calls: []string{"target.use"},
		others: map[string]PackageContext{"target": target}}
	ae := newExtExecutor(t, &extPage{})
	ae.SetPackage(caller)
	var scopes []string
	ae.SetSecretLookup(func(automationID, name string) (string, bool) {
		scopes = append(scopes, automationID+"/"+name)
		return "val-" + automationID, true
	})
	wantOK(t, runExt(t, ae, StepDef{ID: "c", Type: "call_action", Action: "target.use"}))
	if len(scopes) == 0 || scopes[len(scopes)-1] != "target/token" {
		t.Fatalf("secret looked up under %v, want target/token", scopes)
	}
	if getVar(ae, "inner") != "val-target" {
		t.Fatalf("inner = %v", getVar(ae, "inner"))
	}
	if ae.secretScope() != "caller" {
		t.Fatalf("scope not restored: %s", ae.secretScope())
	}
}
