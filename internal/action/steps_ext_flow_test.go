package action

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func setVarStep(id, name string, value interface{}) StepDef {
	return StepDef{ID: id, Type: "set_variable", VariableName: name, Value: value}
}

// ---------------------------------------------------------------------------
// call_fragment
// ---------------------------------------------------------------------------

func TestCallFragmentBindsInputsAndRestores(t *testing.T) {
	ae := newExtExecutor(t, &extPage{})
	ae.SetPackage(&extPkg{id: "site", fragments: map[string]*FragmentDef{
		"greet": {Name: "greet", Steps: []StepDef{setVarStep("s1", "seen", "{{who}}-{{mood}}")}},
	}})
	ae.SetVariable("who", "outer")
	ae.SetVariable("name", "Ada")

	res := runExt(t, ae, StepDef{ID: "c", Type: "call_fragment", Fragment: "greet",
		Inputs: map[string]interface{}{"who": "{{name}}", "mood": "happy"}})
	wantOK(t, res)
	if got := getVar(ae, "seen"); got != "Ada-happy" {
		t.Fatalf("fragment saw %v, want Ada-happy", got)
	}
	if got := getVar(ae, "who"); got != "outer" {
		t.Fatalf("who not restored: %v", got)
	}
	if _, ok := ae.execCtx.GetVariable("mood"); ok {
		t.Fatal("mood should be unset after the fragment")
	}
}

func TestCallFragmentErrors(t *testing.T) {
	ae := newExtExecutor(t, &extPage{})
	wantFail(t, runExt(t, ae, StepDef{ID: "c", Type: "call_fragment", Fragment: "x"}), "needs an automation package")

	req, _ := json.Marshal("q")
	ae.SetPackage(&extPkg{id: "site", fragments: map[string]*FragmentDef{
		"needs": {Name: "needs", Inputs: &InputDef{Required: []json.RawMessage{req}}},
	}})
	wantFail(t, runExt(t, ae, StepDef{ID: "c", Type: "call_fragment", Fragment: "missing"}), "not found")
	wantFail(t, runExt(t, ae, StepDef{ID: "c", Type: "call_fragment"}), "no fragment named")
	wantFail(t, runExt(t, ae, StepDef{ID: "c", Type: "call_fragment", Fragment: "needs"}), "missing required input 'q'")
	wantOK(t, runExt(t, ae, StepDef{ID: "c", Type: "call_fragment", Fragment: "needs", Inputs: map[string]interface{}{"q": "x"}}))
}

func TestCallFragmentRecursionIsBounded(t *testing.T) {
	ae := newExtExecutor(t, &extPage{})
	self := StepDef{ID: "again", Type: "call_fragment", Fragment: "loop", OnError: &ErrorHandlerDef{Action: ErrorActionAbort}}
	ae.SetPackage(&extPkg{id: "site", fragments: map[string]*FragmentDef{
		"loop": {Name: "loop", Steps: []StepDef{self}},
	}})
	res := runExt(t, ae, self)
	if res.Success || !res.Abort {
		t.Fatalf("want aborted failure, got %+v", res)
	}
	if d, _ := ae.execCtx.GetData(extDepthKey); d != 0 {
		t.Fatalf("depth not restored: %v", d)
	}

	// Without an abort policy the depth error surfaces as the step's own.
	ae.SetPackage(&extPkg{id: "site", fragments: map[string]*FragmentDef{
		"loop": {Name: "loop", Steps: []StepDef{{ID: "again", Type: "call_fragment", Fragment: "loop"}}},
	}})
	ae.execCtx.SetData(extDepthKey, maxNestingDepth)
	wantFail(t, runExt(t, ae, StepDef{ID: "top", Type: "call_fragment", Fragment: "loop"}), "nesting deeper than 8")
}

// ---------------------------------------------------------------------------
// call_action
// ---------------------------------------------------------------------------

func TestCallActionRunsStepsAndLoopsInTargetPackage(t *testing.T) {
	other := &extPkg{id: "other", fragments: map[string]*FragmentDef{
		"mark": {Name: "mark", Steps: []StepDef{setVarStep("m", "fromOther", "yes")}},
	}}
	other.actions = map[string]*ActionDef{
		"collect": {
			ActionType: "collect",
			Steps: []StepDef{
				{ID: "frag", Type: "call_fragment", Fragment: "mark"},
				setVarStep("each", "last", "{{item}}"),
				{ID: "count", Type: "update_progress", Increment: "n"},
			},
			Loops: []LoopDef{{ID: "l", Iterator: "things", IndexVar: "i", Steps: []string{"each", "count"}}},
		},
	}
	home := &extPkg{id: "home", other: map[string]*extPkg{"other": other}}
	db := &capTestStorage{}
	ae := newExtExecutor(t, &extPage{})
	ae.db = db
	ae.action.ReachedIndex = 5 // must not leak into the called action's loop
	ae.SetPackage(home)
	prevDef := ae.actionDef

	res := runExt(t, ae, StepDef{ID: "ca", Type: "call_action", Action: "other.collect",
		Inputs: map[string]interface{}{"things": []interface{}{"a", "b", "c"}}})
	wantOK(t, res)
	if getVar(ae, "fromOther") != "yes" {
		t.Fatal("fragment of the target package did not run")
	}
	if getVar(ae, "last") != "c" || getVar(ae, "n") != 3 {
		t.Fatalf("loop ran wrong: last=%v n=%v", getVar(ae, "last"), getVar(ae, "n"))
	}
	if ae.pkg != home || ae.actionDef != prevDef || ae.action.ReachedIndex != 5 || ae.db != db {
		t.Fatal("executor state not restored after call_action")
	}
	if len(db.reachedIndexes) != 0 {
		t.Fatalf("called action wrote the caller's reached index: %v", db.reachedIndexes)
	}
	if _, ok := ae.execCtx.GetVariable("things"); ok {
		t.Fatal("inputs not unbound")
	}
}

func TestCallActionErrors(t *testing.T) {
	ae := newExtExecutor(t, &extPage{})
	wantFail(t, runExt(t, ae, StepDef{ID: "c", Type: "call_action", Action: "x"}), "needs an automation package")
	ae.SetPackage(&extPkg{id: "home"})
	wantFail(t, runExt(t, ae, StepDef{ID: "c", Type: "call_action", Action: "nope"}), "not found")
	wantFail(t, runExt(t, ae, StepDef{ID: "c", Type: "call_action", Action: "ghost.x"}), "not installed")
	wantFail(t, runExt(t, ae, StepDef{ID: "c", Type: "call_action"}), "no action named")
}

func TestCallActionRecursionIsBounded(t *testing.T) {
	p := &extPkg{id: "home"}
	p.actions = map[string]*ActionDef{"self": {ActionType: "self", Steps: []StepDef{
		{ID: "again", Type: "call_action", Action: "self", OnError: &ErrorHandlerDef{Action: ErrorActionAbort}},
	}}}
	ae := newExtExecutor(t, &extPage{})
	ae.SetPackage(p)
	res := runExt(t, ae, StepDef{ID: "top", Type: "call_action", Action: "self"})
	if res.Success || !res.Abort {
		t.Fatalf("want aborted failure, got %+v", res)
	}
	if ae.pkg != p {
		t.Fatal("package not restored")
	}
}

// ---------------------------------------------------------------------------
// for_each
// ---------------------------------------------------------------------------

func TestForEachIteratesAndRestores(t *testing.T) {
	ae := newExtExecutor(t, &extPage{})
	ae.SetVariable("rows", []interface{}{map[string]interface{}{"n": "a"}, map[string]interface{}{"n": "b"}})
	ae.SetVariable("row", "outer")
	res := runExt(t, ae, StepDef{ID: "fe", Type: "for_each", Items: "{{rows}}", As: "row", Steps: []StepDef{
		{ID: "acc", Type: "set_variable", VariableName: "acc", Value: "{{acc}}{{row.n}}{{row_index}}"},
	}})
	wantOK(t, res)
	if res.Data != 2 {
		t.Fatalf("processed %v, want 2", res.Data)
	}
	if got := getVar(ae, "acc"); got != "a0b1" {
		t.Fatalf("acc = %v, want a0b1", got)
	}
	if getVar(ae, "row") != "outer" {
		t.Fatal("as-variable not restored")
	}
	if ae.execCtx.inLoop() {
		t.Fatal("loop depth not restored")
	}
}

func TestForEachVariants(t *testing.T) {
	ae := newExtExecutor(t, &extPage{})
	ae.SetVariable("list", []string{"x", "y", "z"})
	body := []StepDef{{ID: "b", Type: "set_variable", VariableName: "last", Value: "{{item}}"}}

	// Bare path, default "item", BatchSize cap.
	res := runExt(t, ae, StepDef{ID: "fe", Type: "for_each", Items: "list", BatchSize: 2, Steps: body})
	wantOK(t, res)
	if res.Data != 2 || getVar(ae, "last") != "y" {
		t.Fatalf("batch cap: data=%v last=%v", res.Data, getVar(ae, "last"))
	}
	// A JSON array string.
	ae.SetVariable("jsonList", `["p","q"]`)
	wantOK(t, runExt(t, ae, StepDef{ID: "fe", Type: "for_each", Items: "{{jsonList}}", Steps: body}))
	if getVar(ae, "last") != "q" {
		t.Fatalf("json list: last=%v", getVar(ae, "last"))
	}
	// Missing → zero iterations; not a list → error.
	res = runExt(t, ae, StepDef{ID: "fe", Type: "for_each", Items: "{{absent}}", Steps: body})
	wantOK(t, res)
	if res.Data != 0 {
		t.Fatalf("absent items processed %v", res.Data)
	}
	ae.SetVariable("scalar", "null")
	wantFail(t, runExt(t, ae, StepDef{ID: "fe", Type: "for_each", Items: "{{scalar}}", Steps: body}), "cannot be iterated")
}

func TestForEachHonoursCancellationAndAbort(t *testing.T) {
	ae := newExtExecutor(t, &extPage{})
	ae.SetVariable("list", []interface{}{1, 2})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, _ := ae.stepForEach(ctx, StepDef{ID: "fe", Type: "for_each", Items: "list"})
	wantFail(t, res, "context canceled")

	res = runExt(t, ae, StepDef{ID: "fe", Type: "for_each", Items: "list", Steps: []StepDef{
		{ID: "bad", Type: "assert", Condition: "1 == 2", OnError: &ErrorHandlerDef{Action: ErrorActionAbort}},
	}})
	if res.Success || !res.Abort {
		t.Fatalf("abort in body must abort for_each, got %+v", res)
	}
}

// ---------------------------------------------------------------------------
// assert
// ---------------------------------------------------------------------------

func TestAssert(t *testing.T) {
	ae := newExtExecutor(t, &extPage{})
	ae.SetVariable("count", 3)
	wantOK(t, runExt(t, ae, StepDef{ID: "a", Type: "assert", Condition: "count > 2"}))
	wantFail(t, runExt(t, ae, StepDef{ID: "a", Type: "assert", Condition: "count > 5", Description: "need more than five"}), "need more than five")
	wantFail(t, runExt(t, ae, StepDef{ID: "a", Type: "assert", Condition: map[string]interface{}{"variable": "nope", "operator": "exists"}}), "assertion failed")
	wantFail(t, runExt(t, ae, StepDef{ID: "a", Type: "assert"}), "no condition")
}

// ---------------------------------------------------------------------------
// wait_for / waitUntil
// ---------------------------------------------------------------------------

func TestWaitForConditions(t *testing.T) {
	polls := 0
	page := &extPage{
		urlFn: func() string {
			polls++
			if polls >= 3 {
				return "https://site.test/done"
			}
			return "https://site.test/loading"
		},
		has: func(sel string) bool { return sel == "#ready" },
		eval: func(js string) (interface{}, error) {
			if strings.Contains(js, "innerText.includes") {
				return jsOut(strings.Contains(js, `"Welcome"`)), nil
			}
			return nil, nil
		},
	}
	ae := newExtExecutor(t, page)
	cases := []struct {
		name string
		spec WaitSpec
	}{
		{"url", WaitSpec{URLMatches: `/done$`}},
		{"selector", WaitSpec{Selector: "#ready"}},
		{"gone", WaitSpec{Gone: "#spinner"}},
		{"text", WaitSpec{Text: "Welcome"}},
		{"any", WaitSpec{Any: []WaitSpec{{Selector: "#never"}, {Text: "Welcome"}}}},
	}
	for _, c := range cases {
		spec := c.spec
		res := runExt(t, ae, StepDef{ID: "w", Type: "wait_for", Until: &spec, Timeout: 3})
		if !res.Success {
			t.Errorf("%s: %v", c.name, res.Error)
		}
	}
	if polls < 3 {
		t.Fatalf("url condition should have polled, polls=%d", polls)
	}
}

func TestWaitForTemplatedSelector(t *testing.T) {
	ae := newExtExecutor(t, &extPage{has: func(s string) bool { return s == "#row-7" }})
	ae.SetVariable("id", 7)
	wantOK(t, runExt(t, ae, StepDef{ID: "w", Type: "wait_for", Until: &WaitSpec{Selector: "#row-{{id}}"}, Timeout: 1}))
}

func TestWaitForFailures(t *testing.T) {
	ae := newExtExecutor(t, &extPage{has: func(string) bool { return false }})
	res := runExt(t, ae, StepDef{ID: "w", Type: "wait_for", Until: &WaitSpec{Selector: "#never"}, Timeout: 0.3})
	wantFail(t, res, "timed out")
	wantFail(t, runExt(t, ae, StepDef{ID: "w", Type: "wait_for", Until: &WaitSpec{URLMatches: "("}}), "urlMatches")
	wantFail(t, runExt(t, ae, StepDef{ID: "w", Type: "wait_for", Until: &WaitSpec{}}), "empty wait condition")
	wantFail(t, runExt(t, ae, StepDef{ID: "w", Type: "wait_for"}), "no condition")

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := ae.waitUntil(ctx, &WaitSpec{Selector: "#never"}, 10*time.Second); err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("waitUntil must stop on context cancellation, err=%v", err)
	}
	if err := ae.waitUntil(context.Background(), nil, time.Second); err != nil {
		t.Fatalf("nil spec is a no-op, got %v", err)
	}
}

func TestWaitForNetworkIdle(t *testing.T) {
	n := 0
	page := &extPage{eval: func(js string) (interface{}, error) {
		n++
		count := n
		if count > 3 {
			count = 3 // resources stop arriving after the third poll
		}
		return jsOut(map[string]interface{}{"ready": "complete", "n": count}), nil
	}}
	ae := newExtExecutor(t, page)
	start := time.Now()
	wantOK(t, runExt(t, ae, StepDef{ID: "w", Type: "wait_for", Until: &WaitSpec{NetworkIdle: true}, Timeout: 5}))
	if time.Since(start) < networkIdleQuiet {
		t.Fatal("network idle declared before the quiet period")
	}

	busy := &extPage{eval: func(string) (interface{}, error) {
		n++
		return jsOut(map[string]interface{}{"ready": "loading", "n": n}), nil
	}}
	ae = newExtExecutor(t, busy)
	wantFail(t, runExt(t, ae, StepDef{ID: "w", Type: "wait_for", Until: &WaitSpec{NetworkIdle: true}, Timeout: 0.6}), "network idle")
}

func TestSafeModeStopsInsideNestedBodies(t *testing.T) {
	for _, outer := range []StepDef{
		{ID: "fe", Type: "for_each", Items: "list", Steps: []StepDef{
			setVarStep("before", "ran", "yes"),
			{ID: "send", Type: "log", SideEffect: true},
		}},
		{ID: "cf", Type: "call_fragment", Fragment: "f"},
	} {
		ae := newExtExecutor(t, &extPage{})
		ae.SetPackage(&extPkg{id: "site", fragments: map[string]*FragmentDef{"f": {Name: "f", Steps: []StepDef{
			setVarStep("before", "ran", "yes"),
			{ID: "send", Type: "log", SideEffect: true},
		}}}})
		ae.SetSafeMode(true)
		ae.SetVariable("list", []interface{}{1, 2})
		err := ae.executeSteps(context.Background(), []StepDef{outer, setVarStep("after", "after", "yes")})
		if err == nil {
			t.Fatalf("%s: want the safe stop to unwind executeSteps", outer.Type)
		}
		if s := ae.SafeStopped(); s == nil || s.StepID != "send" {
			t.Fatalf("%s: safe stop = %+v", outer.Type, s)
		}
		if getVar(ae, "ran") != "yes" || getVar(ae, "after") != nil {
			t.Fatalf("%s: ran=%v after=%v", outer.Type, getVar(ae, "ran"), getVar(ae, "after"))
		}
	}
}
