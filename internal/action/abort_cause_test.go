package action

import (
	"errors"
	"strings"
	"testing"
)

func TestAbortKeepsCause(t *testing.T) {
	ae := newPkgExecutor(&markPage{}, nil)
	def := &ActionDef{ActionType: "t", SideEffects: "none", Steps: []StepDef{
		{ID: "login", Type: "click", Selector: "#gone", Timeout: 0.01,
			OnError: &ErrorHandlerDef{Action: ErrorActionAbort}},
		{ID: "after", Type: "log", Text: "no"},
	}}
	_, err := ae.ExecuteDef(&StorageAction{ID: "a", TargetPlatform: "p", Type: "t"}, def)
	if !errors.Is(err, ErrAbort) || !strings.Contains(err.Error(), "#gone") || !strings.Contains(err.Error(), "login") {
		t.Fatalf("err = %v, want ErrAbort wrapping the click failure", err)
	}
	if ae.execCtx.GetStepResult("after") != nil {
		t.Fatal("run continued after abort")
	}
}

func TestSaveDataSingleMapMergesForPackages(t *testing.T) {
	def := &ActionDef{ActionType: "t", SideEffects: "read", Steps: []StepDef{
		{ID: "s", Type: "save_data", DataSource: "metrics"},
	}}
	act := &StorageAction{ID: "a", TargetPlatform: "p", Type: "t"}

	ae := newPkgExecutor(nil, &fakePkg{id: "acme"})
	ae.SetVariable("metrics", map[string]interface{}{"points": 10})
	res, err := ae.ExecuteDef(act, def)
	if err != nil || res.ListOutput || len(res.ExtractedItems) != 1 {
		t.Fatalf("packaged single map: res=%+v err=%v", res, err)
	}

	ae2 := newPkgExecutor(nil, &fakePkg{id: "acme"})
	ae2.SetVariable("metrics", []interface{}{map[string]interface{}{"a": 1}, map[string]interface{}{"a": 2}})
	res, err = ae2.ExecuteDef(act, def)
	if err != nil || !res.ListOutput || len(res.ExtractedItems) != 2 {
		t.Fatalf("packaged list: res=%+v err=%v", res, err)
	}
}

// Legacy actions keep master's behaviour: every single-map save is a record.
func TestSaveDataLegacyKeepsRecords(t *testing.T) {
	legacyDef := &ActionDef{ActionType: "t", SideEffects: "read", Steps: []StepDef{
		{ID: "s1", Type: "save_data", DataSource: "one"},
		{ID: "s2", Type: "save_data", DataSource: "two"},
	}}
	for _, pkg := range []PackageContext{nil, &fakePkg{id: "local-foo"}} {
		ae := newPkgExecutor(nil, pkg)
		ae.SetVariable("one", map[string]interface{}{"x": 1})
		ae.SetVariable("two", map[string]interface{}{"x": 2})
		res, err := ae.ExecuteDef(&StorageAction{ID: "a", TargetPlatform: "foo", Type: "t"}, legacyDef)
		if err != nil || !res.ListOutput || len(res.ExtractedItems) != 2 {
			t.Fatalf("pkg=%v: res=%+v err=%v", pkg, res, err)
		}
	}
}
