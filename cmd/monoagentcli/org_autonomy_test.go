package main

import (
	"context"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/orgdecide"
	"github.com/monoes/mono-agent/internal/orgdesign"
	"github.com/monoes/mono-agent/internal/orggrant"
	"github.com/monoes/mono-agent/internal/storage"
)

func TestOrgAutomationRoleLifecycle(t *testing.T) {
	f := newOrgCLIFixture(t)
	f.mustRun(t, "automation", "add", "growth", "--workflow", f.outboundWF, "--alias", "publish_post")
	out := f.mustRun(t, "automation-role", "add", "growth", "--alias", "publish_post", "--reports-to", "lead")
	role := out["role"].(map[string]interface{})
	if role["id"] != "publish-post" || role["reports_to"] != "lead" {
		t.Fatalf("role = %v", role)
	}
	url := out["endpoint_url"].(string)
	d := f.load(t)
	r, _ := d.FindRole("publish-post")
	if r == nil || !r.IsEndpoint() || r.Endpoint.URL != url || r.Automation.WorkflowID != f.outboundWF || r.Type != "automation" {
		t.Fatalf("endpoint role in JSON = %+v", r)
	}
	if err := orgdesign.Validate(d); err != nil {
		t.Fatalf("saved org invalid: %v", err)
	}

	rot := f.mustRun(t, "automation-role", "rotate", "growth", "--role", "publish-post")
	if rot["endpoint_url"] == url || rot["applies"] != "next org start" {
		t.Fatalf("rotate = %v", rot)
	}
	r, _ = f.load(t).FindRole("publish-post")
	if r.Endpoint.URL != rot["endpoint_url"] {
		t.Fatal("rotation not written to the org file")
	}

	if _, err := f.run(t, "automation", "remove", "growth", "--alias", "publish_post"); err == nil {
		t.Fatal("removed an automation an automation role still runs")
	}
	f.mustRun(t, "automation-role", "remove", "growth", "--role", "publish-post")
	if r, _ := f.load(t).FindRole("publish-post"); r != nil {
		t.Fatal("role not removed")
	}
	id := orggrant.EndpointIDFromURL(rot["endpoint_url"].(string))
	db, _, _, _ := (&orgEnv{cfg: f.cfg}).Profile()
	if _, err := orggrant.NewStore(db.DB).LookupEndpoint(t.Context(), id); err == nil {
		t.Fatal("endpoint still live after removing the role")
	}
}

// An alias without an underscore must not become a role id equal to the
// alias (found in a live run: "formatter").
func TestAutomationRoleIDNeverEqualsAlias(t *testing.T) {
	f := newOrgCLIFixture(t)
	f.mustRun(t, "automation", "add", "growth", "--workflow", f.plainWF, "--alias", "formatter")
	out := f.mustRun(t, "automation-role", "add", "growth", "--alias", "formatter", "--reports-to", "lead")
	if id := out["role"].(map[string]interface{})["id"]; id != "formatter-bot" {
		t.Fatalf("role id = %v", id)
	}
	if err := orgdesign.Validate(f.load(t)); err != nil {
		t.Fatalf("saved org invalid: %v", err)
	}
}

func TestOrgAutonomyCommands(t *testing.T) {
	f := newOrgCLIFixture(t)
	show := f.mustRun(t, "autonomy", "show", "growth")
	if show["level"] != "manual" || show["effective_level"] != "manual" || show["daemon_running"] != false {
		t.Fatalf("default autonomy = %v", show)
	}
	set := f.mustRun(t, "autonomy", "set", "growth", "--level", "full", "--tier", "gate=consequential", "--tier", "tool:*=routine", "--policy", "No spend over $50.")
	if set["level"] != "full" || set["tiers"].(map[string]interface{})["gate"] != "consequential" {
		t.Fatalf("set = %v", set)
	}
	if _, err := f.run(t, "autonomy", "set", "growth", "--tier", "everything=routine"); err == nil {
		t.Fatal("unknown decision class accepted")
	}
	if _, err := f.run(t, "autonomy", "set", "growth", "--decider", "parent"); err == nil || !strings.Contains(err.Error(), "holding org") {
		t.Fatalf("parent decider without a holding org: %v", err)
	}
	d := f.load(t)
	if d.Autonomy == nil || d.Autonomy.Level != "full" || d.Autonomy.Policy != "No spend over $50." {
		t.Fatalf("display copy = %+v", d.Autonomy)
	}

	// A hand edit lowering the level is applied on the next save; raising
	// it back in the file is ignored (C-54).
	d.Autonomy.Level = "mid"
	if _, err := orgdesign.Save(f.root, d); err != nil {
		t.Fatal(err)
	}
	f.mustRun(t, "automation", "add", "growth", "--workflow", f.plainWF, "--alias", "summarize")
	if got := f.mustRun(t, "autonomy", "show", "growth")["level"]; got != "mid" {
		t.Fatalf("lowered level = %v", got)
	}
	d = f.load(t)
	d.Autonomy.Level = "full"
	if _, err := orgdesign.Save(f.root, d); err != nil {
		t.Fatal(err)
	}
	f.mustRun(t, "automation", "remove", "growth", "--alias", "summarize")
	if got := f.mustRun(t, "autonomy", "show", "growth")["level"]; got != "mid" {
		t.Fatalf("hand-edited raise applied: %v", got)
	}
	if f.load(t).Autonomy.Level != "mid" {
		t.Fatal("display copy not restored after an ignored raise")
	}

	paused := f.mustRun(t, "autonomy", "pause", "growth", "--for", "30m")
	if paused["paused_until"] == nil || paused["effective_level"] != "manual" {
		t.Fatalf("pause = %v", paused)
	}
	if f.mustRun(t, "autonomy", "resume", "growth")["paused_until"] != nil {
		t.Fatal("resume left the org paused")
	}
	if ds := f.mustRun(t, "autonomy", "decisions", "growth")["decisions"].([]interface{}); len(ds) != 0 {
		t.Fatalf("decisions = %v", ds)
	}
}

// Q8: an org created by an explicit command starts at mid, and a document
// cannot start one at full.
func TestNewOrgStartsAtMid(t *testing.T) {
	f := newOrgCLIFixture(t)
	f.mustRun(t, "create-json", "fresh", "--json", `{"name":"fresh","goal":"g","status":"stopped","schedule":null,"roles":[{"id":"lead","title":"Lead","type":"boss","reports_to":null,"responsibilities":[]}],"autonomy":{"level":"full"}}`)
	if got := f.mustRun(t, "autonomy", "show", "fresh")["level"]; got != "mid" {
		t.Fatalf("new org level = %v", got)
	}
}

func TestOrgGroupInitAndParentDecider(t *testing.T) {
	f := newOrgCLIFixture(t)
	f.mustRun(t, "create-json", "hq", "--json", `{"name":"hq","kind":"holding","goal":"g","status":"stopped","schedule":null,"roles":[{"id":"ceo","title":"CEO","type":"boss","reports_to":null,"responsibilities":[]}],"children":[{"org":"growth","start":"on_demand","budget_share":0.5}]}`)

	out := f.mustRun(t, "group", "init", "hq")
	if out["initiator"] != "ceo" || strings.Join(toStrings(out["children"]), ",") != "growth" {
		t.Fatalf("group init = %v", out)
	}
	hq, err := orgdesign.Load(f.root, "hq")
	if err != nil {
		t.Fatal(err)
	}
	ceo, _ := hq.FindRole("ceo")
	if len(ceo.ToolProviders) != 1 || !strings.Contains(strings.Join(ceo.ToolProviders[0].Allow, ","), "org_start") {
		t.Fatalf("initiator provider = %+v", ceo.ToolProviders)
	}
	if got := ceo.PolicyStrings("approvalTools"); strings.Join(got, ",") != "monoagent__org_start" {
		t.Fatalf("org_start is not a decision: %v", got)
	}
	lead, _ := f.load(t).FindRole("lead")
	if !strings.Contains(strings.Join(lead.Responsibilities, "\n"), "hq:ceo") {
		t.Fatalf("child boss has no report-up line: %v", lead.Responsibilities)
	}

	f.mustRun(t, "autonomy", "set", "growth", "--decider", "parent")
	hq, _ = orgdesign.Load(f.root, "hq")
	ceo, _ = hq.FindRole("ceo")
	allow := strings.Join(ceo.ToolProviders[0].Allow, ",")
	if !strings.Contains(allow, "decision_resolve") || !strings.Contains(allow, "org_start") {
		t.Fatalf("parent decider tools not merged into the initiator's grant: %s", allow)
	}

	if _, err := f.run(t, "group", "status", "growth"); err == nil {
		t.Fatal("group status on a standard org")
	}
}

// The decisions listing carries Jev's distribution and a summary of it, so
// the GUI can show "Jev p=0.93" or "model decided (Jev p=0.62 below 0.8)".
func TestAutonomyDecisionsJevView(t *testing.T) {
	f := newOrgCLIFixture(t)
	db, err := storage.NewDatabase(f.cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	st := orgdecide.NewStore(db.DB)
	ctx := context.Background()
	for _, d := range []orgdecide.Decision{
		{ID: "d-jev", ProfileID: "default", OrgName: "growth", ItemKind: "approval", ItemRef: "a1", ItemHash: "h1", Class: "tool:x", Tier: "consequential",
			Level: "mid", Resolver: "jev:jev-1.13", Verdict: "approved", Rationale: "jev jev-1.13: approve p=0.93 (approve 0.93, deny 0.07)",
			Confidence: 0.9, Probabilities: map[string]float64{"approve": 0.93, "deny": 0.07}, CreatedAt: "2026-09-25T10:00:00Z"},
		{ID: "d-fb", ProfileID: "default", OrgName: "growth", ItemKind: "approval", ItemRef: "a2", ItemHash: "h2", Class: "tool:y", Tier: "consequential",
			Level: "mid", Resolver: "model:m", Verdict: "denied", Rationale: "jev jev-1.13: approve p=0.62 below 0.80 (approve 0.62, deny 0.38); risky",
			Confidence: 0.5, Probabilities: map[string]float64{"approve": 0.62, "deny": 0.38}, CreatedAt: "2026-09-25T10:01:00Z"},
		{ID: "d-model", ProfileID: "default", OrgName: "growth", ItemKind: "approval", ItemRef: "a3", ItemHash: "h3", Class: "tool:z", Tier: "consequential",
			Level: "mid", Resolver: "model:m", Verdict: "approved", Rationale: "fine", CreatedAt: "2026-09-25T10:02:00Z"},
	} {
		d := d
		if err := st.Record(ctx, &d); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	rows := map[string]map[string]interface{}{}
	for _, r := range f.mustRun(t, "autonomy", "decisions", "growth")["decisions"].([]interface{}) {
		m := r.(map[string]interface{})
		rows[m["id"].(string)] = m
	}
	j := rows["d-jev"]
	if j["confidence"] != 0.9 || j["probabilities"].(map[string]interface{})["approve"] != 0.93 {
		t.Fatalf("jev row = %v", j)
	}
	if v := j["jev"].(map[string]interface{}); v["decided"] != true || v["verdict"] != "approve" || v["p"] != 0.93 || v["threshold"] != nil {
		t.Fatalf("jev view = %v", v)
	}
	if v := rows["d-fb"]["jev"].(map[string]interface{}); v["decided"] != false || v["p"] != 0.62 || v["threshold"] != 0.8 {
		t.Fatalf("fallback view = %v", v)
	}
	if _, ok := rows["d-model"]["jev"]; ok {
		t.Fatalf("model row has a jev view: %v", rows["d-model"])
	}
	if _, ok := rows["d-model"]["probabilities"]; ok {
		t.Fatalf("model row has probabilities: %v", rows["d-model"])
	}
}
