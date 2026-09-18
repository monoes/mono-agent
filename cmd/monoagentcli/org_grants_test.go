package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/orgdesign"
	"github.com/monoes/mono-agent/internal/orggrant"
	"github.com/monoes/mono-agent/internal/profiledir"
	"github.com/monoes/mono-agent/internal/storage"
	"github.com/monoes/mono-agent/internal/workflow"
)

type orgCLIFixture struct {
	cfg        *globalConfig
	root       string
	outboundWF string
	plainWF    string
}

// newOrgCLIFixture isolates HOME (profile folders, workflow file store,
// daemon heartbeat), points monomind at the fake script, and seeds one org
// plus two workflows: one with an outbound node, one without.
func newOrgCLIFixture(t *testing.T) *orgCLIFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MONOAGENT_DAEMON_HEARTBEAT", filepath.Join(home, "hb.json"))
	t.Setenv("MONOAGENT_API_ADDR", "")
	fake, err := filepath.Abs(filepath.Join("..", "..", "internal", "monomind", "testdata", "fake-monomind.sh"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("MONOMIND_BIN", fake)

	dbPath := filepath.Join(home, "test.db")
	db, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.ApplyMigrations(); err != nil {
		t.Fatal(err)
	}
	store := workflow.NewSQLiteWorkflowStore(db.DB)
	ctx := context.Background()
	mk := func(name string, nodeType string) string {
		wf := &workflow.Workflow{Name: name, ProfileID: "default"}
		if err := store.CreateWorkflow(ctx, wf); err != nil {
			t.Fatal(err)
		}
		if err := store.SaveWorkflowNodes(ctx, wf.ID, []workflow.WorkflowNode{
			{ID: wf.ID + "-t", WorkflowID: wf.ID, Type: "trigger.manual", Name: "start"},
			{ID: wf.ID + "-n", WorkflowID: wf.ID, Type: nodeType, Name: "step"},
		}); err != nil {
			t.Fatal(err)
		}
		return wf.ID
	}
	f := &orgCLIFixture{
		cfg:        &globalConfig{DBPath: dbPath, ProfileID: "default"},
		root:       profiledir.Root(db.DB, "default"),
		outboundWF: mk("Publish post", "comm.slack"),
		plainWF:    mk("Summarize", "core.set"),
	}
	db.Close()

	doc := orgdesign.NewOrg("growth", "grow", orgdesign.NewOrgOptions{})
	doc.Roles = append(doc.Roles, orgdesign.Role{ID: "writer", Title: "Writer", Type: "specialist", ReportsTo: strPtrCLI("lead"), Responsibilities: []string{}})
	if _, err := orgdesign.Save(f.root, doc); err != nil {
		t.Fatal(err)
	}
	return f
}

func strPtrCLI(s string) *string { return &s }

func (f *orgCLIFixture) run(t *testing.T, args ...string) (map[string]interface{}, error) {
	t.Helper()
	cmd := newOrgCmd(f.cfg)
	cmd.SetArgs(args)
	var runErr error
	out := captureStdout(t, func() { runErr = cmd.Execute() })
	if runErr != nil {
		return nil, runErr
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &m); err != nil {
		t.Fatalf("org %v printed non-JSON %q: %v", args, out, err)
	}
	return m, nil
}

func (f *orgCLIFixture) mustRun(t *testing.T, args ...string) map[string]interface{} {
	t.Helper()
	m, err := f.run(t, args...)
	if err != nil {
		t.Fatalf("org %v: %v", args, err)
	}
	return m
}

func (f *orgCLIFixture) load(t *testing.T) *orgdesign.Doc {
	t.Helper()
	d, err := orgdesign.Load(f.root, "growth")
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestOrgGrantLifecycle(t *testing.T) {
	f := newOrgCLIFixture(t)

	add := f.mustRun(t, "automation", "add", "growth", "--workflow", f.outboundWF, "--alias", "publish_post", "--owned")
	auto := add["automation"].(map[string]interface{})
	if auto["has_outbound_nodes"] != true || auto["workflow_name"] != "Publish post" {
		t.Fatalf("automation view = %v", auto)
	}
	if _, ok := f.load(t).Extra["fence"]; !ok {
		t.Fatal("fence not pre-filled when the first automation was added")
	}

	unassigned := f.mustRun(t, "automation", "unassigned")
	ws := unassigned["workflows"].([]interface{})
	if len(ws) != 1 || ws[0].(map[string]interface{})["id"] != f.plainWF {
		t.Fatalf("unassigned = %v", ws)
	}

	g := f.mustRun(t, "grant", "add", "growth", "--role", "writer", "--automation", "publish_post")
	grant := g["grant"].(map[string]interface{})
	if grant["approval"] != "required" || grant["tier"] != "irreversible" {
		t.Fatalf("outbound workflow grant should default to required/irreversible: %v", grant)
	}
	if g["tool_name"] != "monoagent__automation_publish_post" {
		t.Fatalf("tool_name = %v", g["tool_name"])
	}

	d := f.load(t)
	writer, _ := d.FindRole("writer")
	if len(writer.Automations) != 1 || len(writer.ToolProviders) != 1 {
		t.Fatalf("JSON not updated: %+v", writer)
	}
	p := writer.ToolProviders[0]
	if p.Args[2] != grant["id"] || p.Args[4] != "default" || p.Name != "monoagent" {
		t.Fatalf("provider = %+v", p)
	}
	if got := writer.PolicyStrings("approvalTools"); strings.Join(got, ",") != "monoagent__automation_publish_post" {
		t.Fatalf("approvalTools = %v", got)
	}
	if got := writer.PolicyStrings("denyTools"); strings.Join(got, ",") != "Bash" {
		t.Fatalf("denyTools = %v", got)
	}
	if err := orgdesign.Validate(d); err != nil {
		t.Fatalf("saved org invalid: %v", err)
	}

	// Operator re-enables Bash, then updates the grant: Bash stays allowed,
	// and the command warns.
	writer.SetPolicyStrings("denyTools", nil)
	if _, err := orgdesign.Save(f.root, d); err != nil {
		t.Fatal(err)
	}
	upd := f.mustRun(t, "grant", "add", "growth", "--role", "writer", "--automation", "publish_post", "--approval", "none", "--timeout", "60")
	if upd["grant"].(map[string]interface{})["id"] != grant["id"] {
		t.Fatal("updating a grant changed its id")
	}
	if !strings.Contains(strings.Join(toStrings(upd["warnings"]), "\n"), "can use Bash") {
		t.Fatalf("missing Bash warning: %v", upd["warnings"])
	}
	writer, _ = f.load(t).FindRole("writer")
	if len(writer.PolicyStrings("approvalTools")) != 0 || writer.ToolProviders[0].TimeoutMS != 90000 {
		t.Fatalf("update not applied: approvalTools=%v provider=%+v", writer.PolicyStrings("approvalTools"), writer.ToolProviders[0])
	}

	list := f.mustRun(t, "grant", "list", "growth")
	if n := len(list["grants"].([]interface{})); n != 1 {
		t.Fatalf("grant list has %d grants", n)
	}

	eff := f.mustRun(t, "effective-tools", "growth", "--role", "writer")
	names := map[string]string{}
	for _, it := range eff["tools"].([]interface{}) {
		m := it.(map[string]interface{})
		names[m["name"].(string)] = m["source"].(string)
	}
	if names["monoagent__automation_publish_post"] != "grant" || names["org_send"] != "org" || names["Bash"] != "builtin" {
		t.Fatalf("effective tools = %v", names)
	}
	if _, ok := names["org_complete"]; ok {
		t.Fatal("non-boss role lists org_complete")
	}

	if _, err := f.run(t, "--project", t.TempDir(), "grant", "list", "growth"); err == nil || !strings.Contains(err.Error(), "is not the org root of profile") {
		t.Fatalf("mismatched --project not refused: %v", err)
	}

	f.mustRun(t, "grant", "remove", "growth", "--role", "writer", "--automation", "publish_post")
	writer, _ = f.load(t).FindRole("writer")
	if len(writer.ToolProviders) != 0 || len(writer.Automations) != 0 {
		t.Fatalf("grant remove left JSON behind: %+v", writer)
	}

	f.mustRun(t, "automation", "remove", "growth", "--alias", "publish_post")
	if len(f.load(t).Automations) != 0 {
		t.Fatal("automation not removed")
	}
}

func TestOrgGrantRefusals(t *testing.T) {
	f := newOrgCLIFixture(t)
	if _, err := f.run(t, "automation", "add", "growth", "--workflow", f.plainWF, "--alias", "status"); err == nil {
		t.Fatal("reserved alias accepted")
	}
	if _, err := f.run(t, "automation", "add", "growth", "--workflow", f.plainWF, "--alias", "lead"); err == nil {
		t.Fatal("alias equal to a role id accepted")
	}
	if _, err := f.run(t, "automation", "add", "growth", "--workflow", "nope", "--alias", "x"); err == nil {
		t.Fatal("unknown workflow accepted")
	}
	f.mustRun(t, "automation", "add", "growth", "--workflow", f.plainWF, "--alias", "summarize")
	if _, err := f.run(t, "grant", "add", "growth", "--role", "ghost", "--automation", "summarize"); err == nil {
		t.Fatal("grant to unknown role accepted")
	}
	g := f.mustRun(t, "grant", "add", "growth", "--role", "lead", "--automation", "summarize")
	if gg := g["grant"].(map[string]interface{}); gg["approval"] != "none" || gg["tier"] != "consequential" {
		t.Fatalf("plain workflow grant defaults = %v", gg)
	}
}

// C-3 at the CLI boundary: create-json with a hand-written provider and
// grant id that no row backs saves the org with both stripped.
func TestOrgCreateJSONStripsUnbackedGrants(t *testing.T) {
	f := newOrgCLIFixture(t)
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "orgs", "growth.json"))
	if err != nil {
		t.Fatal(err)
	}
	out := f.mustRun(t, "create-json", "growth", "--json", string(raw))
	kinds := []string{}
	for _, it := range out["reconcile"].([]interface{}) {
		kinds = append(kinds, it.(map[string]interface{})["kind"].(string))
	}
	joined := strings.Join(kinds, ",")
	for _, want := range []string{"grant_spec_stripped", "provider_stripped", "endpoint_unregistered"} {
		if !strings.Contains(joined, want) {
			t.Errorf("reconcile findings %s missing %s", joined, want)
		}
	}
	lead, _ := f.load(t).FindRole("lead")
	if len(lead.ToolProviders) != 0 || len(lead.Automations) != 0 {
		t.Fatalf("unbacked JSON survived create-json: %+v", lead)
	}
}

// C-20: a granted workflow cannot be deleted by accident; --force revokes
// the grant and strips it from the org JSON first.
func TestWorkflowDeleteChecksOrgReferences(t *testing.T) {
	f := newOrgCLIFixture(t)
	f.mustRun(t, "automation", "add", "growth", "--workflow", f.plainWF, "--alias", "summarize")
	f.mustRun(t, "grant", "add", "growth", "--role", "writer", "--automation", "summarize")

	err := checkWorkflowOrgReferences(context.Background(), f.cfg, f.plainWF, false)
	if err == nil || exitCodeFor(err) != 3 || !strings.Contains(err.Error(), "grant to growth:writer") {
		t.Fatalf("unforced delete of a granted workflow: err=%v code=%d", err, exitCodeFor(err))
	}
	if err := checkWorkflowOrgReferences(context.Background(), f.cfg, f.plainWF, true); err != nil {
		t.Fatal(err)
	}
	writer, _ := f.load(t).FindRole("writer")
	if len(writer.Automations) != 0 || len(writer.ToolProviders) != 0 {
		t.Fatalf("forced delete left the grant in the JSON: %+v", writer)
	}
	if err := checkWorkflowOrgReferences(context.Background(), f.cfg, f.plainWF, false); err != nil {
		t.Fatalf("no references left, still refused: %v", err)
	}
}

func TestOrgLegacyListAndMove(t *testing.T) {
	f := newOrgCLIFixture(t)
	legacy := expandPath(legacyOrgProjectRoot)
	old := orgdesign.NewOrg("oldorg", "old", orgdesign.NewOrgOptions{})
	if _, err := orgdesign.Save(legacy, old); err != nil {
		t.Fatal(err)
	}
	stateFile := filepath.Join(orgdesign.OrgsDir(legacy), "oldorg-state.json")
	if err := os.WriteFile(stateFile, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(orgdesign.OrgsDir(legacy), "oldorg", "run-1"), 0o755); err != nil {
		t.Fatal(err)
	}

	list := f.mustRun(t, "legacy", "list")
	if got := toStrings(list["orgs"]); strings.Join(got, ",") != "oldorg" {
		t.Fatalf("legacy orgs = %v", got)
	}
	moved := f.mustRun(t, "legacy", "move", "oldorg")
	if moved["moved_to"] != f.root {
		t.Fatalf("moved_to = %v", moved["moved_to"])
	}
	for _, p := range []string{"oldorg.json", "oldorg-state.json", filepath.Join("oldorg", "run-1")} {
		if _, err := os.Stat(filepath.Join(orgdesign.OrgsDir(f.root), p)); err != nil {
			t.Errorf("%s not moved: %v", p, err)
		}
	}
	if got := toStrings(f.mustRun(t, "legacy", "list")["orgs"]); len(got) != 0 {
		t.Fatalf("legacy list after move = %v", got)
	}
}

func toStrings(v interface{}) []string {
	arr, _ := v.([]interface{})
	out := make([]string, 0, len(arr))
	for _, x := range arr {
		out = append(out, x.(string))
	}
	return out
}

// orgToolScopeOf renders a role's org-tool grant as "tool=org+org,..." .
func orgToolScopeOf(t *testing.T, f *orgCLIFixture, org, role string) string {
	t.Helper()
	db, err := storage.NewDatabase(f.cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	grants, err := orggrant.NewStore(db.DB).ListGrants(context.Background(), "default", org, role)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, g := range grants {
		for _, ot := range g.OrgTools {
			out = append(out, ot.Tool+"="+strings.Join(ot.Orgs, "+"))
		}
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

// Dropping a child from a holding org and re-running `org group init` must
// take that child off the Initiator. Nothing used to shrink an org-tool
// grant, so the Initiator kept org_stop, org_status and org_report over an
// org the holding no longer owns (only org_start was separately blocked,
// by CheckStart).
func TestOrgGroupInitDropsARemovedChild(t *testing.T) {
	f := newOrgCLIFixture(t)
	for _, child := range []string{"sales", "support"} {
		if _, err := orgdesign.Save(f.root, orgdesign.NewOrg(child, "work", orgdesign.NewOrgOptions{})); err != nil {
			t.Fatal(err)
		}
	}
	hq := orgdesign.NewOrg("hq", "own the group", orgdesign.NewOrgOptions{RootRoleID: "initiator", RootRoleTitle: "Initiator"})
	hq.Kind = orgdesign.OrgKindHolding
	hq.ChildOrgs = []orgdesign.ChildOrg{{Org: "sales"}, {Org: "support"}}
	if _, err := orgdesign.Save(f.root, hq); err != nil {
		t.Fatal(err)
	}

	f.mustRun(t, "group", "init", "hq")
	want := "org_report=sales+support,org_start=sales+support,org_status=sales+support,org_stop=sales+support"
	if got := orgToolScopeOf(t, f, "hq", "initiator"); got != want {
		t.Fatalf("scope after first init = %q", got)
	}

	hq, err := orgdesign.Load(f.root, "hq")
	if err != nil {
		t.Fatal(err)
	}
	hq.ChildOrgs = []orgdesign.ChildOrg{{Org: "sales"}}
	if _, err := orgdesign.Save(f.root, hq); err != nil {
		t.Fatal(err)
	}
	f.mustRun(t, "group", "init", "hq")

	want = "org_report=sales,org_start=sales,org_status=sales,org_stop=sales"
	if got := orgToolScopeOf(t, f, "hq", "initiator"); got != want {
		t.Fatalf("scope after removing support = %q, want %q", got, want)
	}
}
