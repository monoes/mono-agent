package main

import (
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/orgdesign"
	"github.com/monoes/mono-agent/internal/orggrant"
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
