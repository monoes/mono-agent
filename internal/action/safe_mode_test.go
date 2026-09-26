package action

import (
	"testing"
)

func TestSafeModeStopsBeforeSideEffect(t *testing.T) {
	ae := newPkgExecutor(nil, nil)
	ae.SetSafeMode(true)
	def := &ActionDef{ActionType: "t", SideEffects: "write", Steps: []StepDef{
		{ID: "fill", Type: "set_variable", Variable: "x", Value: "1"},
		{ID: "save", Type: "set_variable", Variable: "saved", Value: "yes", SideEffect: true, Description: "Save the contact"},
		{ID: "after", Type: "log", Text: "after"},
	}}
	res, err := ae.ExecuteDef(&StorageAction{ID: "a", TargetPlatform: "p", Type: "t"}, def)
	if err != nil || res == nil {
		t.Fatalf("safe stop must succeed: res=%v err=%v", res, err)
	}
	stop := ae.SafeStopped()
	if stop == nil || stop.StepID != "save" || stop.Type != "set_variable" || stop.Description != "Save the contact" {
		t.Fatalf("SafeStopped = %+v", stop)
	}
	if ae.execCtx.GetStepResult("fill") == nil {
		t.Error("step before the side effect did not run")
	}
	if _, ok := ae.execCtx.GetVariable("saved"); ok || ae.execCtx.GetStepResult("after") != nil {
		t.Error("side-effect step or later step ran in safe mode")
	}
}

func TestSafeModeInsideConditionBranchAndLoops(t *testing.T) {
	ae := newPkgExecutor(nil, nil)
	ae.SetSafeMode(true)
	ae.SetVariable("items", []interface{}{"a", "b"})
	def := &ActionDef{ActionType: "t", SideEffects: "write",
		Steps: []StepDef{
			{ID: "cond", Type: "condition", Condition: map[string]interface{}{"variable": "items", "operator": "exists"},
				Then: []string{"send"}, OnError: &ErrorHandlerDef{Action: ErrorActionContinue}},
			{ID: "send", Type: "set_variable", Variable: "sent", Value: true, SideEffect: true},
			{ID: "tail", Type: "log", Text: "tail"},
			{ID: "per_item", Type: "set_variable", Variable: "looped", Value: true},
		},
		Loops: []LoopDef{{ID: "l", Iterator: "items", IndexVar: "i", Steps: []string{"per_item"}}},
	}
	if _, err := ae.ExecuteDef(&StorageAction{ID: "a", TargetPlatform: "p", Type: "t"}, def); err != nil {
		t.Fatalf("err = %v", err)
	}
	if s := ae.SafeStopped(); s == nil || s.StepID != "send" {
		t.Fatalf("SafeStopped = %+v", s)
	}
	for _, v := range []string{"sent", "looped"} {
		if _, ok := ae.execCtx.GetVariable(v); ok {
			t.Errorf("%s ran after the safe stop", v)
		}
	}
	if ae.execCtx.GetStepResult("tail") != nil {
		t.Error("tail ran after the safe stop")
	}
}

func TestSafeModeOffRunsSideEffects(t *testing.T) {
	ae := newPkgExecutor(nil, nil)
	def := &ActionDef{ActionType: "t", SideEffects: "write", Steps: []StepDef{
		{ID: "save", Type: "set_variable", Variable: "saved", Value: "yes", SideEffect: true},
	}}
	if _, err := ae.ExecuteDef(&StorageAction{ID: "a", TargetPlatform: "p", Type: "t"}, def); err != nil {
		t.Fatal(err)
	}
	if ae.SafeStopped() != nil {
		t.Fatal("safe stop without safe mode")
	}
	if v, _ := ae.execCtx.GetVariable("saved"); v != "yes" {
		t.Fatalf("saved = %v", v)
	}
}

func TestSafeModeExtraStops(t *testing.T) {
	imp, _ := coreTrustWorld()
	imp.actions["writer"] = &ActionDef{ActionType: "writer", SideEffects: "write"}
	imp.actions["reader"] = &ActionDef{ActionType: "reader", SideEffects: "read"}
	ae := newPkgExecutor(nil, imp)
	ae.SetSafeMode(true)
	ae.SetVariable("verb", "post")
	cases := []struct {
		step StepDef
		stop bool
	}{
		{StepDef{Type: "page_script", Script: "x.js"}, true},
		{StepDef{Type: "upload"}, true},
		{StepDef{Type: "download"}, true},
		{StepDef{Type: "http_fetch_in_page"}, false},
		{StepDef{Type: "http_fetch_in_page", Method: "get"}, false},
		{StepDef{Type: "http_fetch_in_page", Method: "{{verb}}"}, true},
		{StepDef{Type: "call_action", Action: "writer"}, true},
		{StepDef{Type: "call_action", Action: "reader"}, false},
		{StepDef{Type: "call_action", Action: "missing"}, true},
		{StepDef{Type: "click"}, false},
		{StepDef{Type: "click", SideEffect: true}, true},
	}
	for _, c := range cases {
		if got := ae.safeStopRequired(c.step); got != c.stop {
			t.Errorf("%s %s%s: stop=%v, want %v", c.step.Type, c.step.Method, c.step.Action, got, c.stop)
		}
	}
}

// A condition nested in a for_each body that branches to a top-level step:
// that step runs only from the branch, not again as an initial step.
func TestNestedConditionBranchNotInitial(t *testing.T) {
	ae := newPkgExecutor(nil, nil)
	ae.SetVariable("xs", []interface{}{"a"})
	ae.SetVariable("hits", 0)
	def := &ActionDef{ActionType: "t", SideEffects: "none", Steps: []StepDef{
		{ID: "loop", Type: "for_each", Items: "{{xs}}", Steps: []StepDef{
			{ID: "cond", Type: "condition", Condition: map[string]interface{}{"variable": "xs", "operator": "exists"}, Then: []string{"bump"}},
		}},
		{ID: "bump", Type: "set_variable", Variable: "bumped", Value: "yes", OnSuccess: &SuccessAction{Action: "increment", Variable: "hits"}},
	}}
	if _, err := ae.ExecuteDef(&StorageAction{ID: "a", TargetPlatform: "p", Type: "t"}, def); err != nil {
		t.Fatal(err)
	}
	if v, _ := ae.execCtx.GetVariable("hits"); v != 1 {
		t.Fatalf("branch-only step ran %v times, want 1", v)
	}
}
