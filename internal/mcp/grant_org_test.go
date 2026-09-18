package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/orgdecide"
	"github.com/monoes/mono-agent/internal/orggrant"
	"github.com/monoes/mono-agent/internal/orggroup"
	"github.com/monoes/mono-agent/internal/storage"
)

func newOrgToolServer(t *testing.T, tools []orggrant.OrgTool) (*Server, *storage.Database) {
	t.Helper()
	f := newGrantFixture(t, orggrant.Tool{})
	t.Setenv("MONOMIND_ORG_NAME", "hq")
	t.Setenv("MONOMIND_ORG_ROLE", "ceo")
	g, err := f.store.SetOrgTools(context.Background(), "default", "hq", "ceo", tools)
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer(Options{DBPath: f.server.opts.DBPath, Profile: "default", WorkflowsDir: filepath.Join(t.TempDir(), "wf"), Grant: g.ID})
	t.Cleanup(func() { s.closeRuntime() })
	return s, f.db
}

func TestInitiatorToolsAreScopedAndBudgeted(t *testing.T) {
	s, db := newOrgToolServer(t, []orggrant.OrgTool{
		{Tool: orggroup.ToolOrgStart, Orgs: []string{"sales"}},
		{Tool: orggroup.ToolOrgStatus, Orgs: []string{"sales"}},
	})
	ctx := context.Background()
	var started []string
	oldStart, oldEnv, oldStatus := orgRunStartFunc, groupEnvFunc, orgStatusFunc
	t.Cleanup(func() { orgRunStartFunc, groupEnvFunc, orgStatusFunc = oldStart, oldEnv, oldStatus })
	orgRunStartFunc = func(_ context.Context, _, name, task string) error {
		started = append(started, name+"|"+task)
		return nil
	}
	orgStatusFunc = func(context.Context, string, string) (json.RawMessage, error) {
		return json.RawMessage(`{"status":"idle"}`), nil
	}
	names := []string{}
	for _, d := range s.grantToolDefinitions(ctx) {
		names = append(names, d["name"].(string))
	}
	if strings.Join(names, ",") != "org_start,org_status" {
		t.Fatalf("initiator tools = %v", names)
	}

	if _, err := s.callGrantTool(ctx, orggroup.ToolOrgStart, json.RawMessage(`{"org":"growth"}`)); err == nil || !strings.HasPrefix(err.Error(), codeRefusedGrant) {
		t.Fatalf("started an org outside its children: %v", err)
	}
	// No holding org file exists, so CheckStart refuses: the budget rule
	// runs before anything starts.
	if _, err := s.callGrantTool(ctx, orggroup.ToolOrgStart, json.RawMessage(`{"org":"sales","task":"go"}`)); err == nil || !strings.HasPrefix(err.Error(), codeRefusedCap) || len(started) != 0 {
		t.Fatalf("start without a readable holding org: %v started=%v", err, started)
	}
	var refused int
	_ = db.DB.QueryRow(`SELECT COUNT(*) FROM org_bridge_calls WHERE direction = 'org_start' AND status = 'refused_cap'`).Scan(&refused)
	if refused != 1 {
		t.Fatalf("refusal not audited: %d", refused)
	}
	if out, err := s.callGrantTool(ctx, orggroup.ToolOrgStatus, json.RawMessage(`{"org":"sales"}`)); err != nil || !strings.Contains(out, "idle") {
		t.Fatalf("status: %s %v", out, err)
	}
}

type recordingClient struct{ calls []string }

func (r *recordingClient) Status(context.Context, string, string) (json.RawMessage, error) {
	return nil, nil
}
func (r *recordingClient) Approvals(context.Context, string, string) (json.RawMessage, error) {
	return nil, nil
}
func (r *recordingClient) Questions(context.Context, string, string) (json.RawMessage, error) {
	return nil, nil
}
func (r *recordingClient) Gates(context.Context, string, string) (json.RawMessage, error) {
	return nil, nil
}
func (r *recordingClient) Approve(_ context.Context, _, org, role, action string, approve bool, o monomind.ResolveOptions) error {
	r.calls = append(r.calls, org+":"+role+":"+action+":"+map[bool]string{true: "approve", false: "deny"}[approve]+":"+o.By)
	return nil
}
func (r *recordingClient) Answer(_ context.Context, _, org, id, answer string, _ monomind.ResolveOptions) error {
	r.calls = append(r.calls, org+":"+id+":"+answer)
	return nil
}
func (r *recordingClient) Notify(_ context.Context, _, org, role, subject, body string) error {
	r.calls = append(r.calls, org+":"+role+":"+subject+":"+body)
	return nil
}
func (r *recordingClient) Gate(_ context.Context, _, org, id string, approve bool, text string, _ monomind.ResolveOptions) error {
	r.calls = append(r.calls, org+":"+id+":"+text)
	return nil
}

// A parent decider resolves a child's delegated item; it cannot resolve
// items delegated to someone else or pick a verdict the level forbids.
func TestDecisionToolsResolveDelegatedItems(t *testing.T) {
	s, db := newOrgToolServer(t, []orggrant.OrgTool{
		{Tool: orgdecide.ToolDecisionList, Orgs: []string{"sales"}},
		{Tool: orgdecide.ToolDecisionResolve, Orgs: []string{"sales"}},
	})
	ctx := context.Background()
	client := &recordingClient{}
	old := decisionClient
	decisionClient = client
	t.Cleanup(func() { decisionClient = old })

	store := orgdecide.NewStore(db.DB)
	item := orgdecide.Item{Kind: orgdecide.KindApproval, Ref: "lead:org_complete:1", Requester: "lead", Class: "org_complete", Tier: "consequential", Action: "org_complete", Summary: "lead wants to use org_complete"}
	mine, err := store.Delegate(ctx, "default", "sales", "full", "hq", "ceo", item, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	other, _ := store.Delegate(ctx, "default", "sales", "full", "sales", "lead", item, time.Now().Add(time.Minute))

	out, err := s.callGrantTool(ctx, orgdecide.ToolDecisionList, nil)
	if err != nil || !strings.Contains(out, mine.ID) || strings.Contains(out, other.ID) {
		t.Fatalf("decision_list = %s %v", out, err)
	}
	if _, err := s.callGrantTool(ctx, orgdecide.ToolDecisionResolve, json.RawMessage(`{"id":"`+other.ID+`","verdict":"approve","rationale":"x"}`)); err == nil {
		t.Fatal("resolved a decision delegated to another role")
	}
	if _, err := s.callGrantTool(ctx, orgdecide.ToolDecisionResolve, json.RawMessage(`{"id":"`+mine.ID+`","verdict":"escalate","rationale":"x"}`)); err == nil {
		t.Fatal("escalate accepted at full")
	}
	if _, err := s.callGrantTool(ctx, orgdecide.ToolDecisionResolve, json.RawMessage(`{"id":"`+mine.ID+`","verdict":"approve","rationale":"work is done"}`)); err != nil {
		t.Fatal(err)
	}
	if len(client.calls) != 1 || client.calls[0] != "sales:lead:org_complete:approve:parent:hq:ceo" {
		t.Fatalf("resolution = %v", client.calls)
	}
	ds, _ := store.List(ctx, "default", "sales", orgdecide.DecisionFilter{})
	if len(ds) != 1 || ds[0].Resolver != "parent:hq:ceo" || ds[0].Verdict != orgdecide.VerdictApproved {
		t.Fatalf("decision rows = %+v", ds)
	}
	if _, err := s.callGrantTool(ctx, orgdecide.ToolDecisionResolve, json.RawMessage(`{"id":"`+mine.ID+`","verdict":"deny","rationale":"again"}`)); err == nil {
		t.Fatal("resolved twice")
	}
}
