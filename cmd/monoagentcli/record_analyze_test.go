package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/action"
	"github.com/monoes/mono-agent/internal/recordanalyze"
	"github.com/monoes/mono-agent/internal/recording"
)

const recordAnalyzeAnswer = `{
  "automation": {"id": "example-go", "name": "Example Go"},
  "action": {
    "actionType": "press_go",
    "description": "Press the Go button",
    "sideEffects": "write",
    "steps": [
      {"id": "open", "type": "navigate", "url": "https://example.com/rec1"},
      {"id": "go", "type": "click", "configKey": "page.go_button", "intent": "the Go button", "sideEffect": true}
    ]
  },
  "selectors": {"page.go_button": {"candidates": [{"css": "button", "score": 0.8}, {"text": "Go", "score": 0.6}]}},
  "names": {"automation": "example-go", "action": "press_go", "fragment": "press_go"}
}`

// stubRecordAI makes analyze answer with canned JSON instead of monomind.
func stubRecordAI(t *testing.T, answers ...string) *int {
	t.Helper()
	calls := 0
	prev := recordAnalyzeRunner
	recordAnalyzeRunner = func(string, string, time.Duration) recordanalyze.Runner {
		return recordanalyze.RunnerFunc(func(context.Context, string) (string, error) {
			calls++
			return answers[min(calls, len(answers))-1], nil
		})
	}
	t.Cleanup(func() { recordAnalyzeRunner = prev })
	return &calls
}

func analyzeRec1(t *testing.T) string {
	t.Helper()
	recordTestHome(t, "rec1")
	stubRecordAI(t, recordAnalyzeAnswer)
	out, err := runRecordCLI(t, true, "analyze", "rec1")
	if err != nil {
		t.Fatalf("analyze: %v\n%s", err, out)
	}
	var res struct {
		DraftDir string              `json:"draftDir"`
		Draft    recordanalyze.Draft `json:"draft"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("analyze JSON: %v\n%s", err, out)
	}
	if res.Draft.Action != "press_go" || !res.Draft.IsNew || res.Draft.TargetAutomation != "example-go" {
		t.Fatalf("draft = %+v", res.Draft)
	}
	for _, is := range res.Draft.Lint {
		if is.Severity == "error" {
			t.Errorf("lint error: %+v", is)
		}
	}
	return res.DraftDir
}

func TestRecordAnalyzeJSON(t *testing.T) {
	dir := analyzeRec1(t)
	if !strings.HasSuffix(dir, "/.monoagent/recording-drafts/"+filepathBase(dir)) {
		t.Errorf("draft dir = %s", dir)
	}
}

func filepathBase(p string) string { return p[strings.LastIndex(p, "/")+1:] }

func TestRecordAnalyzeErrorsAsJSON(t *testing.T) {
	recordTestHome(t)
	out, err := runRecordCLI(t, true, "analyze", "nope")
	if err == nil {
		t.Fatal("unknown recording accepted")
	}
	var body map[string]any
	if json.Unmarshal([]byte(out), &body) != nil || body["error"] == nil {
		t.Errorf("want {\"error\"} on stdout, got %q", out)
	}
}

func TestRecordAnalyzeVerifyNoBridge(t *testing.T) {
	dir := analyzeRec1(t)
	prev := recordVerifyExec
	recordVerifyExec = func(context.Context, string, bool) (recordanalyze.ExecFunc, error) {
		return nil, errors.New("browser bridge not connected")
	}
	t.Cleanup(func() { recordVerifyExec = prev })
	out, err := runRecordCLI(t, true, "verify", dir)
	if err == nil || !strings.Contains(out, `"error":"browser bridge not connected"`) {
		t.Errorf("err %v out %s", err, out)
	}
}

func TestRecordAnalyzeVerifySafeStop(t *testing.T) {
	dir := analyzeRec1(t)
	prev := recordVerifyExec
	recordVerifyExec = func(_ context.Context, id string, _ bool) (recordanalyze.ExecFunc, error) {
		if id != "example-go" {
			t.Errorf("automation = %s", id)
		}
		return func(_ context.Context, def *action.ActionDef, _ action.PackageContext, _ map[string]any, safe bool, _ action.SelectorObserver) recordanalyze.RunOutcome {
			if !safe {
				t.Error("verify without --full must be safe")
			}
			return recordanalyze.RunOutcome{
				Events:   []action.ExecutionEvent{{Type: "step_start", StepID: "open"}, {Type: "step_complete", StepID: "open"}},
				Result:   &action.ExecutionResult{},
				SafeStop: &action.SafeStop{StepID: "go", Type: "click"},
			}
		}, nil
	}
	t.Cleanup(func() { recordVerifyExec = prev })
	out, err := runRecordCLI(t, true, "verify", dir)
	if err != nil {
		t.Fatalf("verify: %v\n%s", err, out)
	}
	var rep recordanalyze.VerifyReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatal(err)
	}
	if !rep.OK || rep.StoppedAt == nil || len(rep.Steps) != 2 || rep.Steps[1].Status != recordanalyze.StatusStopped {
		t.Errorf("report = %+v", rep)
	}
	if _, err := runRecordCLI(t, true, "verify", "/etc"); err == nil {
		t.Error("a path outside recording-drafts was accepted")
	}
}

func TestRecordAnalyzeSaveActionAndWorkflow(t *testing.T) {
	dir := analyzeRec1(t)
	out, err := runRecordCLI(t, true, "save", dir)
	if err != nil {
		t.Fatalf("save: %v\n%s", err, out)
	}
	var res recordanalyze.SaveResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if res.Automation != "example-go" || res.NodeType != "example-go.press_go" || res.Version == "" || len(res.Warnings) != 0 {
		t.Errorf("save = %+v", res)
	}
	list, err := recording.List()
	if err != nil || len(list) != 1 || list[0].Automation != "example-go" {
		t.Errorf("recording link: %+v %v", list, err)
	}

	out, err = runRecordCLI(t, true, "save", dir, "--as", "workflow", "--name", "press go flow")
	if err != nil {
		t.Fatalf("save workflow: %v\n%s", err, out)
	}
	res = recordanalyze.SaveResult{}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if res.WorkflowID == "" || res.NodeType != "example-go.press_go_flow" {
		t.Errorf("workflow save = %+v", res)
	}
}

func TestRecordAnalyzeBuildWorkflow(t *testing.T) {
	wf := buildRecordedWorkflow("n", "d", "p1", []recordanalyze.WorkflowNode{{Name: "a", Type: "x.a"}, {Name: "b", Type: "x.b"}})
	if len(wf.Nodes) != 3 || wf.Nodes[0].Type != "trigger.manual" || len(wf.Connections) != 2 {
		t.Fatalf("wf = %+v", wf)
	}
	if wf.Connections[1].SourceNodeID != wf.Nodes[1].ID || wf.Connections[1].TargetNodeID != wf.Nodes[2].ID || wf.ProfileID != "p1" {
		t.Errorf("wiring = %+v", wf.Connections)
	}
}
