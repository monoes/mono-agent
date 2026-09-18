package orggrant

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/orgdesign"
	"github.com/monoes/mono-agent/internal/storage"
	"github.com/monoes/mono-agent/internal/workflow"
)

func newTestStore(t *testing.T) (*Store, *sql.DB) {
	t.Helper()
	db, err := storage.NewDatabase(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.ApplyMigrations(); err != nil {
		t.Fatal(err)
	}
	return NewStore(db.DB), db.DB
}

func goldenDoc(t *testing.T) *orgdesign.Doc {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "orgs", "growth.json"))
	if err != nil {
		t.Fatal(err)
	}
	var d orgdesign.Doc
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	return &d
}

var testOpts = GenOptions{ProfileID: "default", CLIPath: "/opt/bin/monoagentcli", APIAddr: DefaultAPIAddr}

func findingKinds(rep *Report) string {
	var kinds []string
	for _, f := range rep.Findings {
		kinds = append(kinds, f.Kind+"@"+f.Role)
	}
	return strings.Join(kinds, ",")
}

func TestIDs(t *testing.T) {
	g := NewGrantID()
	if !strings.HasPrefix(g, "grt_") || len(g) != 26 {
		t.Fatalf("grant id %q", g)
	}
	e := NewEndpointID()
	if !ValidEndpointID(e) {
		t.Fatalf("endpoint id %q not valid", e)
	}
	if ValidEndpointID("ep_short") || ValidEndpointID(strings.ToUpper(e)) {
		t.Fatal("malformed endpoint ids accepted")
	}
	if got := EndpointIDFromURL(EndpointURL("", e)); got != e {
		t.Fatalf("EndpointIDFromURL = %q", got)
	}
}

func TestUpsertGrantKeepsIDAndAppliesDefaults(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	in := GrantInput{ProfileID: "default", OrgName: "growth", RoleID: "lead", Tool: Tool{Alias: "publish_post", WorkflowID: "wf-publish", Wait: true}}
	g1, err := s.UpsertGrant(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	tool := g1.Automation()
	if tool.Tool != "automation_publish_post" || tool.Timeout != 600 || tool.MaxCallsPerRun != 20 || tool.MaxCallsPerDay != 200 || tool.MaxOutputBytes != 16384 || tool.Approval != "none" || tool.Mode != "run" {
		t.Fatalf("defaults not applied: %+v", tool)
	}
	in.Tool.Approval = "required"
	g2, err := s.UpsertGrant(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if g2.ID != g1.ID || g2.Automation().Approval != "required" {
		t.Fatalf("upsert did not update in place: %+v", g2)
	}
	if err := s.RevokeGrant(ctx, "default", "growth", "lead", "publish_post"); err != nil {
		t.Fatal(err)
	}
	if gs, _ := s.ListGrants(ctx, "default", "growth", ""); len(gs) != 0 {
		t.Fatalf("revoked grant still listed: %v", gs)
	}
	b, err := s.ResolveBundle(ctx, g1.ID)
	if err != nil || b.RoleID != "lead" || len(b.Grants) != 0 {
		t.Fatalf("bundle of revoked row = %+v, %v", b, err)
	}
}

func TestReconcileGeneratesBlocksFromRows(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	d := goldenDoc(t)
	g, err := s.UpsertGrant(ctx, GrantInput{ProfileID: "default", OrgName: "growth", RoleID: "lead",
		Tool: Tool{Alias: "publish_post", WorkflowID: "wf-publish", Wait: true, Approval: "required", Timeout: 900}})
	if err != nil {
		t.Fatal(err)
	}
	ep, err := s.CreateEndpoint(ctx, "default", "growth", "publisher-bot", "wf-publish")
	if err != nil {
		t.Fatal(err)
	}

	rep, err := Reconcile(ctx, s, d, testOpts)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Changed {
		t.Fatalf("expected changes, got %s", findingKinds(rep))
	}
	lead, _ := d.FindRole("lead")
	if len(lead.ToolProviders) != 1 {
		t.Fatalf("providers = %+v", lead.ToolProviders)
	}
	p := lead.ToolProviders[0]
	if p.Command != "/opt/bin/monoagentcli" || strings.Join(p.Args, " ") != "mcp --grant "+g.ID+" --profile default" || p.TimeoutMS != 930000 {
		t.Fatalf("provider = %+v", p)
	}
	if strings.Join(p.Allow, ",") != "automation_output,automation_publish_post,automation_status" {
		t.Fatalf("allow = %v", p.Allow)
	}
	if lead.Automations[0].TimeoutSeconds != 900 {
		t.Fatalf("display copy not rewritten from row: %+v", lead.Automations[0])
	}
	if got := lead.PolicyStrings("approvalTools"); strings.Join(got, ",") != "monoagent__automation_publish_post" {
		t.Fatalf("approvalTools = %v", got)
	}
	bot, _ := d.FindRole("publisher-bot")
	if bot.Endpoint.URL != EndpointURL(DefaultAPIAddr, ep.ID) {
		t.Fatalf("endpoint url = %s", bot.Endpoint.URL)
	}

	// Second pass is a no-op.
	rep2, err := Reconcile(ctx, s, d, testOpts)
	if err != nil {
		t.Fatal(err)
	}
	if rep2.Changed || len(rep2.Findings) != 0 {
		t.Fatalf("second reconcile not idempotent: %s", findingKinds(rep2))
	}
}

// C-3: a JSON edit cannot mint a grant. The golden file names a grant id
// and a provider that no row backs; reconcile strips both and reports it.
func TestReconcileStripsUnbackedJSON(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	d := goldenDoc(t)
	lead, _ := d.FindRole("lead")
	lead.ToolProviders = append(lead.ToolProviders, orgdesign.ToolProvider{
		Kind: "mcp-stdio", Name: "sneaky", Command: "monoagentcli", Args: []string{"mcp", "--allow-mutations"},
	}, orgdesign.ToolProvider{
		Kind: "mcp-stdio", Name: "fs", Command: "npx", Args: []string{"-y", "some-fs-server"},
	})

	rep, err := Reconcile(ctx, s, d, testOpts)
	if err != nil {
		t.Fatal(err)
	}
	kinds := findingKinds(rep)
	for _, want := range []string{"grant_spec_stripped@lead", "provider_stripped@lead", "approval_tools_updated@lead", "endpoint_unregistered@publisher-bot"} {
		if !strings.Contains(kinds, want) {
			t.Errorf("missing finding %s in %s", want, kinds)
		}
	}
	if len(lead.Automations) != 0 {
		t.Fatalf("unbacked grant spec kept: %+v", lead.Automations)
	}
	if len(lead.ToolProviders) != 1 || lead.ToolProviders[0].Name != "fs" {
		t.Fatalf("providers after reconcile = %+v", lead.ToolProviders)
	}
	if got := lead.PolicyStrings("approvalTools"); len(got) != 0 {
		t.Fatalf("managed approvalTools kept: %v", got)
	}
	if got := lead.PolicyStrings("denyTools"); strings.Join(got, ",") != "Bash" {
		t.Fatalf("unmanaged policy lost: %v", got)
	}
	if gs, _ := s.ListGrants(ctx, "default", "growth", ""); len(gs) != 0 {
		t.Fatalf("reconcile created rows: %v", gs)
	}
}

func TestReconcileRevokesRowsTheJSONDropped(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	d := goldenDoc(t)
	if _, err := s.UpsertGrant(ctx, GrantInput{ProfileID: "default", OrgName: "growth", RoleID: "lead", Tool: Tool{Alias: "publish_post", WorkflowID: "wf-publish"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertGrant(ctx, GrantInput{ProfileID: "default", OrgName: "growth", RoleID: "ghost", Tool: Tool{Alias: "publish_post", WorkflowID: "wf-publish"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateEndpoint(ctx, "default", "growth", "ghost-bot", "wf-publish"); err != nil {
		t.Fatal(err)
	}
	d.Automations[0].WorkflowID = "wf-other"
	d.Roles[1].Automation.WorkflowID = "wf-other"

	rep, err := Reconcile(ctx, s, d, testOpts)
	if err != nil {
		t.Fatal(err)
	}
	kinds := findingKinds(rep)
	for _, want := range []string{"grant_revoked@lead", "grant_revoked@ghost", "endpoint_revoked@ghost-bot"} {
		if !strings.Contains(kinds, want) {
			t.Errorf("missing %s in %s", want, kinds)
		}
	}
	if gs, _ := s.ListGrants(ctx, "default", "growth", ""); len(gs) != 0 {
		t.Fatalf("rows survived: %v", gs)
	}
}

func TestEndpointRotationGrace(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	now := time.Now()
	s.now = func() time.Time { return now }
	old, err := s.CreateEndpoint(ctx, "default", "growth", "bot", "wf")
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := s.RotateEndpoint(ctx, "default", "growth", "bot")
	if err != nil {
		t.Fatal(err)
	}
	if fresh.RotatedFrom != old.ID {
		t.Fatalf("rotated_from = %q", fresh.RotatedFrom)
	}
	if _, err := s.LookupEndpoint(ctx, old.ID); err != nil {
		t.Fatalf("old id refused inside grace window: %v", err)
	}
	s.now = func() time.Time { return now.Add(RotationGrace + time.Second) }
	if _, err := s.LookupEndpoint(ctx, old.ID); err != ErrNotFound {
		t.Fatalf("old id accepted after grace: %v", err)
	}
	if _, err := s.LookupEndpoint(ctx, fresh.ID); err != nil {
		t.Fatalf("new id refused: %v", err)
	}
	live, err := s.LiveEndpoint(ctx, "default", "growth", "bot")
	if err != nil || live.ID != fresh.ID {
		t.Fatalf("live = %+v, %v", live, err)
	}
}

func TestOutboundNodes(t *testing.T) {
	wf := &workflow.Workflow{Nodes: []workflow.WorkflowNode{
		{Type: "trigger.manual"},
		{Type: "comm.email_read"},
		{Type: "comm.slack", Name: "notify"},
		{Type: "http.request", Config: map[string]interface{}{"method": "GET"}},
		{Type: "http.request", Name: "post", Config: map[string]interface{}{"method": "POST"}},
		{Type: "service.notion", Disabled: true},
	}}
	got := OutboundNodes(wf)
	if strings.Join(got, "|") != "notify (comm.slack)|post (http.request)" {
		t.Fatalf("outbound = %v", got)
	}
	if GrantTier(got) != orgdesign.TierIrreversible || GrantTier(nil) != orgdesign.TierConsequential {
		t.Fatal("tier mapping wrong")
	}
}

func TestRenameOrgMovesRows(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	if _, err := s.UpsertGrant(ctx, GrantInput{ProfileID: "default", OrgName: "growth", RoleID: "lead", Tool: Tool{Alias: "a", WorkflowID: "wf"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.RenameOrg(ctx, "default", "growth", "growth2"); err != nil {
		t.Fatal(err)
	}
	if gs, _ := s.ListGrants(ctx, "default", "growth2", ""); len(gs) != 1 {
		t.Fatalf("grant not moved: %v", gs)
	}
}

// insertDelegation writes one pending org_delegations row. orgdecide owns
// the table but imports this package, so the test writes it directly.
func insertDelegation(t *testing.T, db *sql.DB, id, org, deciderOrg string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO org_delegations (id, profile_id, org_name, item_kind, item_ref, item_json, level, decider_org, decider_role, status, created_at, deadline_at)
		 VALUES (?, 'default', ?, 'approval', 'appr-1', '{}', 'mid', ?, 'lead', 'pending', '2026-09-18T10:00:00Z', '2026-09-18T11:00:00Z')`,
		id, org, deciderOrg); err != nil {
		t.Fatal(err)
	}
}

// A pending delegation names the org twice — the org the item belongs to
// and the org of the deciding role. A rename that moves neither hides the
// item from its decider for good: PendingFor stops listing it and
// SweepDelegations cannot resolve it.
func TestRenameOrgMovesDelegations(t *testing.T) {
	s, db := newTestStore(t)
	ctx := context.Background()
	insertDelegation(t, db, "d-own", "growth", "hq")
	insertDelegation(t, db, "d-decide", "sales", "growth")
	if err := s.RenameOrg(ctx, "default", "growth", "growth2"); err != nil {
		t.Fatal(err)
	}
	var org, decider string
	if err := db.QueryRow(`SELECT org_name FROM org_delegations WHERE id = 'd-own'`).Scan(&org); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT decider_org FROM org_delegations WHERE id = 'd-decide'`).Scan(&decider); err != nil {
		t.Fatal(err)
	}
	if org != "growth2" || decider != "growth2" {
		t.Fatalf("delegation left behind: org_name = %q, decider_org = %q", org, decider)
	}
}

func delegationStatus(t *testing.T, db *sql.DB, id string) string {
	t.Helper()
	var st string
	if err := db.QueryRow(`SELECT status FROM org_delegations WHERE id = ?`, id).Scan(&st); err != nil {
		t.Fatal(err)
	}
	return st
}

// insertAutonomy writes one org_autonomy row at level.
func insertAutonomy(t *testing.T, db *sql.DB, org, level string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO org_autonomy (profile_id, org_name, level, decider_json, updated_at, updated_by)
		 VALUES ('default', ?, ?, '{"kind":"boss"}', '2026-09-18T10:00:00Z', 'cli')`, org, level); err != nil {
		t.Fatal(err)
	}
}

// Q8: deleting an org must leave nothing that grants power, or the next
// org of the same name silently inherits the deleted one's level, decider,
// tier overrides, policy and pause instead of starting at mid —
// ensureNewOrgAutonomy returns early whenever a row already exists.
func TestRevokeOrgClearsAutonomyAndPendingDelegations(t *testing.T) {
	s, db := newTestStore(t)
	ctx := context.Background()
	insertAutonomy(t, db, "growth", "full")
	insertAutonomy(t, db, "sales", "full")
	insertDelegation(t, db, "d-own", "growth", "hq")
	insertDelegation(t, db, "d-decide", "sales", "growth")
	insertDelegation(t, db, "d-other", "sales", "hq")

	if err := s.RevokeOrg(ctx, "default", "growth"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM org_autonomy WHERE profile_id = 'default' AND org_name = 'growth'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("autonomy row survived org delete: %d rows", n)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM org_autonomy WHERE org_name = 'sales'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatal("another org's autonomy row was cleared")
	}
	if got := delegationStatus(t, db, "d-own"); got != "expired" {
		t.Fatalf("delegation of the deleted org = %q", got)
	}
	if got := delegationStatus(t, db, "d-decide"); got != "expired" {
		t.Fatalf("delegation waiting on the deleted decider = %q", got)
	}
	if got := delegationStatus(t, db, "d-other"); got != "pending" {
		t.Fatalf("unrelated delegation = %q", got)
	}
}

// A rotated-away id stays usable for RotationGrace so a delivery already
// in flight is not lost, but an explicit revoke ends that grace.
// LookupEndpoint is the only auth on the delivery path, so otherwise
// rotating a leaked id and then removing the role — or deleting the org —
// leaves the leaked id accepting POSTs and starting runs for five minutes.
func TestRevokeEndsRotationGrace(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	now := time.Now()
	s.now = func() time.Time { return now }

	leaked, err := s.CreateEndpoint(ctx, "default", "growth", "bot", "wf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RotateEndpoint(ctx, "default", "growth", "bot"); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeEndpoint(ctx, "default", "growth", "bot"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LookupEndpoint(ctx, leaked.ID); err != ErrNotFound {
		t.Fatalf("rotated-away id still accepted after revoking the role: %v", err)
	}

	leaked2, err := s.CreateEndpoint(ctx, "default", "growth", "bot2", "wf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RotateEndpoint(ctx, "default", "growth", "bot2"); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeOrg(ctx, "default", "growth"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LookupEndpoint(ctx, leaked2.ID); err != ErrNotFound {
		t.Fatalf("rotated-away id still accepted after deleting the org: %v", err)
	}
}

func orgToolScope(g Grant) string {
	var out []string
	for _, ot := range g.OrgTools {
		out = append(out, ot.Tool+"="+strings.Join(ot.Orgs, "+"))
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

// Nothing ever shrank an org-tool grant: MergeOrgTools only adds, so
// removing a child from a holding org's children and re-running
// `org group init` left its Initiator holding org_stop, org_status and
// org_report over the org the holding no longer owns.
func TestReconcileNarrowsOrgToolScopeToTheConfig(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	d := &orgdesign.Doc{
		Name:      "hq",
		Kind:      orgdesign.OrgKindHolding,
		ChildOrgs: []orgdesign.ChildOrg{{Org: "sales"}},
		Roles:     []orgdesign.Role{{ID: "initiator"}},
	}
	if _, err := s.SetOrgTools(ctx, "default", "hq", "initiator", []OrgTool{
		{Tool: "org_start", Orgs: []string{"sales", "growth"}},
		{Tool: "org_stop", Orgs: []string{"growth"}},
		{Tool: "decision_list", Orgs: []string{"hq"}},
	}); err != nil {
		t.Fatal(err)
	}

	rep, err := Reconcile(ctx, s, d, testOpts)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(findingKinds(rep), FindingOrgToolsNarrowed+"@initiator") {
		t.Fatalf("no narrowing reported: %s", findingKinds(rep))
	}
	gs, err := s.ListGrants(ctx, "default", "hq", "initiator")
	if err != nil || len(gs) != 1 {
		t.Fatalf("grants = %v, %v", gs, err)
	}
	if got := orgToolScope(gs[0]); got != "decision_list=hq,org_start=sales" {
		t.Fatalf("scope after reconcile = %q", got)
	}
	init, _ := d.FindRole("initiator")
	if len(init.ToolProviders) != 1 || strings.Join(init.ToolProviders[0].Allow, ",") != "decision_list,org_start" {
		t.Fatalf("provider allow list = %+v", init.ToolProviders)
	}

	// Idempotent: a second pass finds nothing left to narrow.
	rep2, err := Reconcile(ctx, s, d, testOpts)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(findingKinds(rep2), FindingOrgToolsNarrowed) {
		t.Fatalf("narrowing repeated: %s", findingKinds(rep2))
	}
}

// The last org leaving a role's scope leaves no org-tool grant at all, so
// no provider keeps pointing at a row that grants nothing.
func TestReconcileRevokesAnEmptiedOrgToolGrant(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	d := &orgdesign.Doc{Name: "hq", Kind: orgdesign.OrgKindHolding, Roles: []orgdesign.Role{{ID: "initiator"}}}
	if _, err := s.SetOrgTools(ctx, "default", "hq", "initiator", []OrgTool{{Tool: "org_stop", Orgs: []string{"growth"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := Reconcile(ctx, s, d, testOpts); err != nil {
		t.Fatal(err)
	}
	if gs, _ := s.ListGrants(ctx, "default", "hq", "initiator"); len(gs) != 0 {
		t.Fatalf("emptied grant survived: %v", gs)
	}
	if init, _ := d.FindRole("initiator"); len(init.ToolProviders) != 0 {
		t.Fatalf("provider kept: %+v", init.ToolProviders)
	}
}
