package action

import (
	"context"
	"reflect"
	"testing"
)

func selPkg(entries map[string]*SelectorEntry) *fakePkg {
	return &fakePkg{id: "acme", selectors: entries}
}

func TestPkgSelectorFirstCandidate(t *testing.T) {
	page := &markPage{present: map[string]bool{"#save": true, ".save": true}}
	obs := &obsRecord{}
	ae := newPkgExecutor(page, selPkg(map[string]*SelectorEntry{
		"save": {Candidates: []SelectorCandidate{{CSS: "#save"}, {CSS: ".save"}}},
	}))
	ae.SetSelectorObserver(obs)
	el, err := ae.resolveElement(StepDef{ID: "s", Type: "click", ConfigKey: "save", Timeout: 0.05})
	if err != nil || el == nil {
		t.Fatalf("el=%v err=%v", el, err)
	}
	want := []string{"acme/save idx=0 ok=true healed=false"}
	if !reflect.DeepEqual(obs.calls, want) {
		t.Fatalf("observed %v, want %v", obs.calls, want)
	}
}

func TestPkgSelectorHealedByLaterCandidate(t *testing.T) {
	page := &markPage{present: map[string]bool{"//button[.='Save']": true}}
	obs := &obsRecord{}
	ae := newPkgExecutor(page, selPkg(map[string]*SelectorEntry{
		"save": {Candidates: []SelectorCandidate{{CSS: "#gone"}, {XPath: "//button[.='Save']"}}},
	}))
	ae.SetSelectorObserver(obs)
	res, err := ae.stepFindElement(context.Background(), StepDef{ID: "s", Type: "find_element", ConfigKey: "save", Timeout: 0.05})
	if err != nil || !res.Success {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if ae.execCtx.GetElement("s") == nil {
		t.Error("find_element did not store the element")
	}
	want := []string{"acme/save idx=1 ok=true healed=true"}
	if !reflect.DeepEqual(obs.calls, want) {
		t.Fatalf("observed %v, want %v", obs.calls, want)
	}
}

func TestPkgSelectorAriaAndTextViaMarker(t *testing.T) {
	page := &markPage{ariaHits: map[string]bool{
		`{"name":"Email","role":"textbox"}`: true,
		`{"text":"Publish"}`:                true,
	}}
	obs := &obsRecord{}
	ae := newPkgExecutor(page, selPkg(map[string]*SelectorEntry{
		"email":   {Candidates: []SelectorCandidate{{Aria: &AriaSelector{Role: "textbox", Name: "Email"}}}},
		"publish": {Candidates: []SelectorCandidate{{CSS: "#nope"}, {Text: "Publish"}}},
	}))
	ae.SetSelectorObserver(obs)
	if el, err := ae.resolveElement(StepDef{ID: "e", ConfigKey: "email", Timeout: 0.05}); err != nil || el == nil {
		t.Fatalf("aria: el=%v err=%v", el, err)
	}
	if sel := ae.resolveConfigSelector("publish"); sel == "" || !page.marked[sel] {
		t.Fatalf("text: selector %q not a live marker (%v)", sel, page.marked)
	}
	ae.releaseSelectorMarkers()
	if len(page.marked) != 0 || page.cleaned == 0 {
		t.Fatalf("markers not released: %v", page.marked)
	}
	want := []string{"acme/email idx=0 ok=true healed=false", "acme/publish idx=1 ok=true healed=true"}
	if !reflect.DeepEqual(obs.calls, want) {
		t.Fatalf("observed %v, want %v", obs.calls, want)
	}
}

func TestPkgSelectorMissFallsBackToAlternatives(t *testing.T) {
	page := &markPage{present: map[string]bool{"#alt": true}}
	obs := &obsRecord{}
	ae := newPkgExecutor(page, selPkg(map[string]*SelectorEntry{
		"save": {Candidates: []SelectorCandidate{{CSS: "#gone"}}},
	}))
	ae.SetSelectorObserver(obs)
	if el, err := ae.resolveElement(StepDef{ID: "s", ConfigKey: "save", Alternatives: []string{"#alt"}, Timeout: 0.05}); err != nil || el == nil {
		t.Fatalf("el=%v err=%v", el, err)
	}
	if _, err := ae.resolveElement(StepDef{ID: "s2", ConfigKey: "save", Timeout: 0.05}); err == nil {
		t.Fatal("expected a miss")
	}
	want := []string{"acme/save idx=-1 ok=true healed=true", "acme/save idx=-1 ok=false healed=false"}
	if !reflect.DeepEqual(obs.calls, want) {
		t.Fatalf("observed %v, want %v", obs.calls, want)
	}
}

func TestLegacyConfigKeyUnchangedWithoutPackage(t *testing.T) {
	// No package, no config manager: the legacy "resolved to no selector".
	ae := newPkgExecutor(&markPage{}, nil)
	if _, err := ae.resolveElement(StepDef{ID: "s", ConfigKey: "save"}); err == nil ||
		err.Error() != `config key "save" resolved to no selector` {
		t.Fatalf("err = %v", err)
	}
}
