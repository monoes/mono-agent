package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/orgdesign"
	"github.com/monoes/mono-agent/internal/orggrant"
)

// seedProfileOrgPower gives the fixture's org a grant, an automation role
// with a live endpoint, and an enforced autonomy row.
func seedProfileOrgPower(t *testing.T, f *orgCLIFixture) string {
	t.Helper()
	f.mustRun(t, "automation", "add", "growth", "--workflow", f.outboundWF, "--alias", "publish_post")
	f.mustRun(t, "grant", "add", "growth", "--role", "writer", "--automation", "publish_post")
	out := f.mustRun(t, "automation-role", "add", "growth", "--alias", "publish_post", "--reports-to", "lead")
	f.mustRun(t, "autonomy", "set", "growth", "--level", "mid")
	return orggrant.EndpointIDFromURL(out["endpoint_url"].(string))
}

func hasMonoagentProvider(d *orgdesign.Doc) bool {
	for _, r := range d.Roles {
		for _, p := range r.ToolProviders {
			if p.Name == orgdesign.ProviderName {
				return true
			}
		}
	}
	return false
}

func revokedCounts(t *testing.T, m map[string]interface{}) map[string]float64 {
	t.Helper()
	raw, ok := m["revoked"].(map[string]interface{})
	if !ok {
		t.Fatalf("no revoked block: %v", m)
	}
	out := map[string]float64{}
	for k, v := range raw {
		out[k] = v.(float64)
	}
	return out
}

// C-24: deleting a profile must not leave its orgs holding grants, live
// endpoint ids, or enforced autonomy, nor its orgs and org daemon running.
func TestOrgTeardownProfile(t *testing.T) {
	f := newOrgCLIFixture(t)
	endpointID := seedProfileOrgPower(t, f)
	stopLog := filepath.Join(t.TempDir(), "stops.log")
	t.Setenv("FAKE_STOP_LOG", stopLog)

	preview := f.mustRun(t, "teardown-profile", "--dry-run")
	if preview["dry_run"] != true {
		t.Fatalf("preview = %v", preview)
	}
	got := revokedCounts(t, preview)
	if got["grants"] != 1 || got["endpoints"] != 1 || got["autonomy"] != 1 {
		t.Fatalf("dry-run footprint = %v", got)
	}
	if running := preview["running_orgs"].([]interface{}); len(running) != 1 || running[0] != "growth" {
		t.Fatalf("dry-run running orgs = %v", running)
	}
	if _, err := os.Stat(stopLog); err == nil {
		t.Fatal("dry run stopped an org")
	}
	db, _, _, err := (&orgEnv{cfg: f.cfg}).Profile()
	if err != nil {
		t.Fatal(err)
	}
	store := orggrant.NewStore(db.DB)
	if _, err := store.LookupEndpoint(t.Context(), endpointID); err != nil {
		t.Fatalf("dry run revoked the endpoint: %v", err)
	}

	out := f.mustRun(t, "teardown-profile")
	if got := revokedCounts(t, out); got["grants"] != 1 || got["endpoints"] != 1 || got["autonomy"] != 1 {
		t.Fatalf("revoked = %v", got)
	}
	if stopped := out["stopped_orgs"].([]interface{}); len(stopped) != 1 || stopped[0] != "growth" {
		t.Fatalf("stopped orgs = %v", stopped)
	}
	if b, _ := os.ReadFile(stopLog); strings.TrimSpace(string(b)) != "growth" {
		t.Fatalf("org stop requests = %q, want growth only", b)
	}
	if serve := out["org_serve"].(map[string]interface{}); serve["status"] != "not-running" {
		t.Fatalf("org_serve = %v", serve)
	}
	if _, err := store.LookupEndpoint(t.Context(), endpointID); err != orggrant.ErrNotFound {
		t.Fatalf("endpoint still accepted after teardown: %v", err)
	}
	if g, _ := store.ListProfileGrants(t.Context(), "default"); len(g) != 0 {
		t.Fatalf("grants survived teardown: %d", len(g))
	}
	d := f.load(t)
	if hasMonoagentProvider(d) {
		t.Fatal("org file still names a provider for a revoked grant")
	}
	// The automation role keeps its URL (reconcile never deletes roles), but
	// the id in it no longer authenticates a delivery.
	if r, _ := d.FindRole("publish-post"); r == nil || orggrant.EndpointIDFromURL(r.Endpoint.URL) != endpointID {
		t.Fatalf("automation role = %+v", r)
	}
}

func TestOrgTeardownProfileRefusesProject(t *testing.T) {
	f := newOrgCLIFixture(t)
	if _, err := f.run(t, "--project", f.root, "teardown-profile"); err == nil {
		t.Fatal("teardown-profile accepted --project")
	}
}

// The GUI's folder move stops the old folder's orgs and daemon first.
func TestOrgServeStopWithNoDaemon(t *testing.T) {
	f := newOrgCLIFixture(t)
	out := f.mustRun(t, "serve", "--stop")
	if out["status"] != "not-running" || out["root"] != f.root {
		t.Fatalf("serve --stop = %v", out)
	}
	if stopped := out["stopped_orgs"].([]interface{}); len(stopped) != 1 || stopped[0] != "growth" {
		t.Fatalf("stopped orgs = %v", stopped)
	}
	if _, err := f.run(t, "serve", "--stop", "--foreground"); err == nil {
		t.Fatal("--stop --foreground accepted")
	}
}

// After a folder move the org files carry provider blocks written for the
// old install; `org reconcile` regenerates them from the rows.
func TestOrgReconcileRewritesStaleProviders(t *testing.T) {
	f := newOrgCLIFixture(t)
	f.mustRun(t, "automation", "add", "growth", "--workflow", f.outboundWF, "--alias", "publish_post")
	f.mustRun(t, "grant", "add", "growth", "--role", "writer", "--automation", "publish_post")

	d := f.load(t)
	r, _ := d.FindRole("writer")
	if r == nil || len(r.ToolProviders) == 0 {
		t.Fatalf("writer has no provider: %+v", r)
	}
	r.ToolProviders[0].Command = "/old/place/monoagentcli"
	r.ToolProviders[0].Args = []string{"mcp", "--grant", "grt_stale", "--profile", "someone-else"}
	if _, err := orgdesign.Save(f.root, d); err != nil {
		t.Fatal(err)
	}

	out := f.mustRun(t, "reconcile")
	orgs := out["orgs"].([]interface{})
	if len(orgs) != 1 || orgs[0].(map[string]interface{})["saved"] != true {
		t.Fatalf("reconcile = %v", out)
	}
	r, _ = f.load(t).FindRole("writer")
	p := r.ToolProviders[0]
	if p.Command != selfExecutable() || p.Args[len(p.Args)-1] != "default" || p.Args[2] == "grt_stale" {
		t.Fatalf("provider not regenerated: %+v", p)
	}

	again := f.mustRun(t, "reconcile")
	if again["orgs"].([]interface{})[0].(map[string]interface{})["saved"] != false {
		t.Fatalf("second reconcile rewrote an up-to-date file: %v", again)
	}
}
