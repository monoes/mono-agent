package recordanalyze

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/monoes/mono-agent/internal/action"
)

func formDraft(t *testing.T) string {
	t.Helper()
	res, err := Analyze(context.Background(), loadFixture(t, "form-submit"),
		AnalyzeOptions{Home: t.TempDir(), Runner: &stubRunner{answers: []string{answer(t, "form-submit")}}})
	if err != nil {
		t.Fatal(err)
	}
	return res.DraftDir
}

func TestVerifySafeModeReportAndHealing(t *testing.T) {
	dir := formDraft(t)
	var gotSafe bool
	var gotInputs map[string]any
	exec := func(_ context.Context, def *action.ActionDef, pkg action.PackageContext, inputs map[string]any, safe bool, obs action.SelectorObserver) RunOutcome {
		gotSafe, gotInputs = safe, inputs
		obs.ObserveSelector(pkg.ID(), "contact.email_input", 1, true, true) // healed by the aria candidate
		obs.ObserveSelector(pkg.ID(), "contact.name_input", 0, true, false)
		ev := func(typ, id string) action.ExecutionEvent { return action.ExecutionEvent{Type: typ, StepID: id} }
		return RunOutcome{
			Events: []action.ExecutionEvent{ev("step_start", "open"), ev("step_complete", "open"),
				ev("step_start", "email"), ev("step_complete", "email"), ev("step_start", "name"), ev("step_complete", "name"),
				ev("step_start", "password")},
			Result:   &action.ExecutionResult{FailedItems: []action.FailedItem{{StepID: "password", Error: errors.New("secret account_password not in vault")}}},
			SafeStop: &action.SafeStop{StepID: "save", Type: "click"},
		}
	}
	rep, err := Verify(context.Background(), dir, VerifyOptions{Exec: exec})
	if err != nil {
		t.Fatal(err)
	}
	if !gotSafe || gotInputs["email"] != "jane@example.com" {
		t.Errorf("safe=%v inputs=%v", gotSafe, gotInputs)
	}
	want := map[string]string{"open": StatusPass, "email": StatusHealed, "name": StatusPass, "password": StatusFail, "save": StatusStopped}
	for _, s := range rep.Steps {
		if want[s.ID] != s.Status {
			t.Errorf("step %s = %s (%s), want %s", s.ID, s.Status, s.Message, want[s.ID])
		}
	}
	if rep.Steps[1].Selector != `aria:textbox[name="Email"]` {
		t.Errorf("selector = %q", rep.Steps[1].Selector)
	}
	if rep.Steps[4].Message != "would click the Save button" {
		t.Errorf("stop message = %q", rep.Steps[4].Message)
	}
	if rep.OK || rep.StoppedAt == nil || rep.StoppedAt.StepID != "save" {
		t.Errorf("report = %+v", rep)
	}
	if len(rep.Healed) != 1 || rep.Healed[0] != "contact.email_input" {
		t.Errorf("healed = %v", rep.Healed)
	}
	var sels map[string]action.SelectorEntry
	if err := readJSON(filepath.Join(dir, "selectors.json"), &sels); err != nil {
		t.Fatal(err)
	}
	e := sels["contact.email_input"]
	if e.Candidates[0].Aria == nil || e.Candidates[1].CSS == "" || e.VerifiedAt == "" {
		t.Errorf("promotion = %+v", e)
	}
	if sels["contact.name_input"].VerifiedAt == "" || sels["contact.save_button"].VerifiedAt != "" {
		t.Errorf("verifiedAt stamps wrong: %+v", sels)
	}
}

func TestVerifyFullPassAndSkips(t *testing.T) {
	dir := formDraft(t)
	exec := func(_ context.Context, def *action.ActionDef, _ action.PackageContext, _ map[string]any, safe bool, _ action.SelectorObserver) RunOutcome {
		if safe {
			t.Error("--full ran in safe mode")
		}
		var evs []action.ExecutionEvent
		for _, s := range def.Steps {
			evs = append(evs, action.ExecutionEvent{Type: "step_start", StepID: s.ID}, action.ExecutionEvent{Type: "step_complete", StepID: s.ID})
		}
		return RunOutcome{Events: evs, Result: &action.ExecutionResult{}}
	}
	rep, err := Verify(context.Background(), dir, VerifyOptions{Full: true, Exec: exec})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.OK || rep.StoppedAt != nil || len(rep.Steps) != 5 {
		t.Errorf("report = %+v", rep)
	}
}

func TestVerifyRefusedRunSkipsAll(t *testing.T) {
	dir := formDraft(t)
	exec := func(context.Context, *action.ActionDef, action.PackageContext, map[string]any, bool, action.SelectorObserver) RunOutcome {
		return RunOutcome{Err: errors.New("validation: unknown_step_type")}
	}
	rep, err := Verify(context.Background(), dir, VerifyOptions{Exec: exec})
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK || rep.Error == "" || rep.Steps[0].Status != StatusSkipped {
		t.Errorf("report = %+v", rep)
	}
}

func TestVerifyNoBridge(t *testing.T) {
	dir := formDraft(t)
	if _, err := Verify(context.Background(), dir, VerifyOptions{}); err == nil || err.Error() != "browser bridge not connected" {
		t.Errorf("err = %v", err)
	}
	if _, err := Verify(context.Background(), t.TempDir(), VerifyOptions{}); err == nil {
		t.Error("non-draft dir accepted")
	}
}

func TestPromoteHealedNoSelectors(t *testing.T) {
	dir := t.TempDir()
	if got, err := PromoteHealed(dir, []Observation{{Key: "a", Index: 1, OK: true, Healed: true}}); err != nil || got != nil {
		t.Errorf("got %v %v", got, err)
	}
	_ = os.WriteFile(filepath.Join(dir, "selectors.json"), []byte(`{"a":{"candidates":[{"css":"#x"},{"css":"#y"}]}}`), 0o644)
	got, err := PromoteHealed(dir, []Observation{{Key: "a", Index: -1, OK: true, Healed: true}})
	if err != nil || len(got) != 0 {
		t.Errorf("jev heal (index -1) must not reorder: %v %v", got, err)
	}
}

func TestDraftSourceServesDraft(t *testing.T) {
	inner := &draftSource{id: "other", name: "x", raw: []byte(`{"o":1}`)}
	s := &draftSource{inner: inner, id: "acme", name: "a", raw: []byte(`{}`)}
	if b, err := s.Load("acme", "a"); err != nil || string(b) != "{}" {
		t.Errorf("draft load %s %v", b, err)
	}
	if b, err := s.Load("other", "x"); err != nil || string(b) != `{"o":1}` {
		t.Errorf("delegate %s %v", b, err)
	}
	l, _ := s.List()
	if len(l) != 2 || l[1] != "acme/a" {
		t.Errorf("list = %v", l)
	}
}
