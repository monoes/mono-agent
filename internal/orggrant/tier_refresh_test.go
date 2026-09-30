package orggrant

import (
	"context"
	"testing"

	"github.com/monoes/mono-agent/internal/orgdesign"
	"github.com/monoes/mono-agent/internal/workflow"
)

// wfLoader serves one workflow, wf-publish, with the given node types.
func wfLoader(types ...string) WorkflowLoader {
	return func(_ context.Context, id string) (*workflow.Workflow, error) {
		if id != "wf-publish" {
			return nil, nil
		}
		wf := &workflow.Workflow{ID: id}
		for _, typ := range types {
			wf.Nodes = append(wf.Nodes, workflow.WorkflowNode{Type: typ, Name: "step"})
		}
		return wf, nil
	}
}

func storedTier(t *testing.T, s *Store) string {
	t.Helper()
	gs, err := s.ListGrants(context.Background(), "default", "growth", "lead")
	if err != nil || len(gs) != 1 {
		t.Fatalf("grants = %v, %v", gs, err)
	}
	return gs[0].Automation().Tier
}

// #284: a granted workflow that gains an outbound node raises its grants'
// stored tier; one that loses it never lowers the tier.
func TestRaiseTiersOnlyRaises(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	if _, err := s.UpsertGrant(ctx, GrantInput{ProfileID: "default", OrgName: "growth", RoleID: "lead",
		Tool: Tool{Alias: "publish_post", WorkflowID: "wf-publish", Tier: orgdesign.TierConsequential}}); err != nil {
		t.Fatal(err)
	}
	grants, _ := s.ListGrants(ctx, "default", "growth", "")

	raised, err := s.RaiseTiers(ctx, grants, wfLoader("core.set"))
	if err != nil || len(raised) != 0 || storedTier(t, s) != orgdesign.TierConsequential {
		t.Fatalf("no outbound node: raised %v, %v, tier %s", raised, err, storedTier(t, s))
	}

	raised, err = s.RaiseTiers(ctx, grants, wfLoader("core.set", "comm.slack"))
	if err != nil || len(raised) != 1 || raised[0].From != orgdesign.TierConsequential || raised[0].To != orgdesign.TierIrreversible {
		t.Fatalf("outbound node added: raised %+v, %v", raised, err)
	}
	if got := storedTier(t, s); got != orgdesign.TierIrreversible {
		t.Fatalf("stored tier = %s, want irreversible", got)
	}

	grants, _ = s.ListGrants(ctx, "default", "growth", "")
	raised, err = s.RaiseTiers(ctx, grants, wfLoader("core.set"))
	if err != nil || len(raised) != 0 || storedTier(t, s) != orgdesign.TierIrreversible {
		t.Fatalf("outbound node removed: raised %v, %v, tier %s (want no downgrade)", raised, err, storedTier(t, s))
	}

	// A workflow that cannot be read leaves the grant alone.
	if raised, err := s.RaiseTiers(ctx, grants, wfLoader()); err != nil || len(raised) != 0 {
		t.Fatalf("missing workflow: %v, %v", raised, err)
	}
}

// Reconcile with a workflow loader raises the tier, reports it, and the
// grant's display copy follows.
func TestReconcileRaisesGrantTier(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	d := goldenDoc(t)
	if _, err := s.UpsertGrant(ctx, GrantInput{ProfileID: "default", OrgName: "growth", RoleID: "lead",
		Tool: Tool{Alias: "publish_post", WorkflowID: "wf-publish", Tier: orgdesign.TierConsequential}}); err != nil {
		t.Fatal(err)
	}
	opts := testOpts
	opts.Workflow = wfLoader("core.set", "comm.slack")
	rep, err := Reconcile(ctx, s, d, opts)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range rep.Findings {
		found = found || (f.Kind == FindingGrantTierRaised && f.Role == "lead")
	}
	lead, _ := d.FindRole("lead")
	if !found || lead.Automations[0].Tier != orgdesign.TierIrreversible || storedTier(t, s) != orgdesign.TierIrreversible {
		t.Fatalf("findings %s, display tier %q, stored %q", findingKinds(rep), lead.Automations[0].Tier, storedTier(t, s))
	}

	opts.Workflow = wfLoader("core.set")
	if _, err := Reconcile(ctx, s, d, opts); err != nil {
		t.Fatal(err)
	}
	if lead, _ = d.FindRole("lead"); lead.Automations[0].Tier != orgdesign.TierIrreversible {
		t.Fatalf("display tier lowered to %q", lead.Automations[0].Tier)
	}
}
