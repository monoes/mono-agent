package recordanalyze

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/action"
	"github.com/monoes/mono-agent/internal/automation"
)

func draftFrom(t *testing.T, home, fixture, ans string) string {
	t.Helper()
	res, err := Analyze(context.Background(), loadFixture(t, fixture),
		AnalyzeOptions{Home: home, Runner: &stubRunner{answers: []string{ans}}})
	if err != nil {
		t.Fatal(err)
	}
	return res.DraftDir
}

func openReg(t *testing.T, home string) *automation.Registry {
	t.Helper()
	reg, err := automation.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func TestSaveActionNewAndExisting(t *testing.T) {
	home := t.TempDir()
	reg := openReg(t, home)
	dir := draftFrom(t, home, "form-submit", answer(t, "form-submit"))
	var linked []string
	link := func(rec, id string) error { linked = append(linked, rec+"→"+id); return nil }

	res, err := Save(context.Background(), reg, dir, SaveOptions{LinkRecording: link})
	if err != nil {
		t.Fatal(err)
	}
	if res.Automation != "acme-crm" || res.Action != "create_contact" || res.NodeType != "acme-crm.create_contact" || res.Version == "" {
		t.Fatalf("result = %+v", res)
	}
	if len(linked) != 1 || linked[0] != "form-submit→acme-crm" {
		t.Errorf("linked = %v", linked)
	}
	p, err := reg.Get("acme-crm")
	if err != nil {
		t.Fatal(err)
	}
	def, err := p.Action("create_contact")
	if err != nil {
		t.Fatal(err)
	}
	if def.Provenance["recording"] != "form-submit" {
		t.Errorf("provenance = %v", def.Provenance)
	}
	if _, err := os.Stat(filepath.Join(dir, DraftFile)); err != nil {
		t.Error("save deleted the draft")
	}

	// Again, renamed, into the now-existing automation: patch bump.
	res2, err := Save(context.Background(), reg, dir, SaveOptions{Automation: "acme-crm", Name: "Create contact v2"})
	if err != nil {
		t.Fatal(err)
	}
	if res2.Action != "create_contact_v2" || res2.Version == res.Version {
		t.Errorf("second save = %+v (first %s)", res2, res.Version)
	}
	if !strings.Contains(strings.Join(res2.Warnings, " "), "not linked") {
		t.Errorf("missing link warning: %v", res2.Warnings)
	}
	p, _ = reg.Get("acme-crm")
	if len(p.Manifest.Actions) != 2 {
		t.Errorf("actions = %v", p.Manifest.Actions)
	}

	if _, err := Save(context.Background(), reg, dir, SaveOptions{New: "Bad Id"}); err == nil {
		t.Error("invalid --new accepted")
	}
	if _, err := Save(context.Background(), reg, dir, SaveOptions{New: "acme-crm"}); err == nil {
		t.Error("--new of an existing id accepted")
	}
	if _, err := Save(context.Background(), reg, dir, SaveOptions{As: "nope"}); err == nil {
		t.Error("bad --as accepted")
	}
}

func TestSaveActionUnderNewID(t *testing.T) {
	home := t.TempDir()
	reg := openReg(t, home)
	dir := draftFrom(t, home, "form-submit", answer(t, "form-submit"))
	res, err := Save(context.Background(), reg, dir, SaveOptions{New: "my-crm"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := reg.Get("my-crm")
	if err != nil || res.NodeType != "my-crm.create_contact" {
		t.Fatalf("res %+v err %v", res, err)
	}
	def, _ := p.Action("create_contact")
	if def.Automation != "my-crm" {
		t.Errorf("automation field = %q", def.Automation)
	}
}

func TestSaveFragment(t *testing.T) {
	home := t.TempDir()
	reg := openReg(t, home)
	dir := draftFrom(t, home, "form-submit", answer(t, "form-submit"))
	first, err := Save(context.Background(), reg, dir, SaveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	res, err := Save(context.Background(), reg, dir, SaveOptions{As: SaveAsFragment, Automation: "acme-crm"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != "fill_contact_form" || res.NodeType != "" || res.Version == first.Version {
		t.Errorf("result = %+v", res)
	}
	p, _ := reg.Get("acme-crm")
	f, err := p.Fragment("fill_contact_form")
	if err != nil || len(f.Steps) != 5 {
		t.Fatalf("fragment %+v err %v", f, err)
	}
	// Saving the same fragment again gets a suffixed name.
	res, err = Save(context.Background(), reg, dir, SaveOptions{As: SaveAsFragment, Automation: "acme-crm"})
	if err != nil || res.Action != "fill_contact_form_2" {
		t.Errorf("second fragment = %+v %v", res, err)
	}
}

func TestSaveWorkflowSplitsAtNavigations(t *testing.T) {
	home := t.TempDir()
	reg := openReg(t, home)
	ans := strings.Replace(answer(t, "login-multipage"), `{"id": "latest"`,
		`{"id": "orders_page", "type": "navigate", "url": "https://shop.example.test/account/orders"}, {"id": "latest"`, 1)
	dir := draftFrom(t, home, "login-multipage", ans)
	var gotName string
	var gotNodes []WorkflowNode
	create := func(_ context.Context, name, _ string, nodes []WorkflowNode) (string, error) {
		gotName, gotNodes = name, nodes
		return "wf-1", nil
	}
	res, err := Save(context.Background(), reg, dir, SaveOptions{As: SaveAsWorkflow, CreateWorkflow: create})
	if err != nil {
		t.Fatal(err)
	}
	if res.WorkflowID != "wf-1" || len(res.Actions) != 2 || res.Actions[1] != "latest_order_total_part2" {
		t.Fatalf("result = %+v", res)
	}
	if gotName != "get the total of my latest order" || len(gotNodes) != 2 || gotNodes[1].Type != "example-shop.latest_order_total_part2" {
		t.Errorf("workflow %q nodes %+v", gotName, gotNodes)
	}
	p, _ := reg.Get("example-shop")
	d2, err := p.Action("latest_order_total_part2")
	if err != nil || d2.Steps[0].ID != "orders_page" || len(d2.Steps) != 3 {
		t.Errorf("part2 = %+v %v", d2, err)
	}
	if _, err := Save(context.Background(), reg, dir, SaveOptions{As: SaveAsWorkflow}); err == nil {
		t.Error("workflow save without a store accepted")
	}
}

func TestSplitAndUsedInputs(t *testing.T) {
	steps := []action.StepDef{{ID: "a", Type: "navigate"}, {ID: "b", Type: "type", Value: "{{email}}"},
		{ID: "c", Type: "navigate"}, {ID: "d", Type: "navigate"}, {ID: "e", Type: "click"}}
	chunks := splitAtNavigations(steps)
	if len(chunks) != 2 || len(chunks[0]) != 2 || len(chunks[1]) != 3 {
		t.Fatalf("chunks = %+v", chunks)
	}
	in := &action.InputDef{Required: []jsonRaw{jsonRaw(`{"name":"email"}`), jsonRaw(`"other"`)}}
	got := usedInputs(in, chunks[0])
	if len(got.Required) != 1 {
		t.Errorf("used = %s", got.Required)
	}
	if bumpPatch("1.2.9") != "1.2.10" || bumpPatch("x") != "0.1.0" {
		t.Error("bumpPatch")
	}
}
