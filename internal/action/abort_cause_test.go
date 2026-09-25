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

func TestSaveDataSingleMapMerges(t *testing.T) {
	ae := newPkgExecutor(nil, nil)
	ae.SetVariable("metrics", map[string]interface{}{"points": 10})
	ae.SetVariable("rows", []interface{}{map[string]interface{}{"a": 1}, map[string]interface{}{"a": 2}})
	def := &ActionDef{ActionType: "t", SideEffects: "read", Steps: []StepDef{
		{ID: "s", Type: "save_data", DataSource: "metrics"},
	}}
	res, err := ae.ExecuteDef(&StorageAction{ID: "a", TargetPlatform: "p", Type: "t"}, def)
	if err != nil || res.ListOutput || len(res.ExtractedItems) != 1 {
		t.Fatalf("single map: res=%+v err=%v", res, err)
	}

	ae2 := newPkgExecutor(nil, nil)
	ae2.SetVariable("rows", []interface{}{map[string]interface{}{"a": 1}, map[string]interface{}{"a": 2}})
	def.Steps[0].DataSource = "rows"
	res, err = ae2.ExecuteDef(&StorageAction{ID: "a", TargetPlatform: "p", Type: "t"}, def)
	if err != nil || !res.ListOutput || len(res.ExtractedItems) != 2 {
		t.Fatalf("list: res=%+v err=%v", res, err)
	}
}
