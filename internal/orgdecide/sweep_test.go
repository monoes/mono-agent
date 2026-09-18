package orgdecide

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/orgdesign"
)

// delegated sets up an org at full with a boss decider and one delegation
// whose deadline has already passed, so the next sweep decides its fate.
func delegated(t *testing.T, org *fakeOrg, dec *scriptedDecider) (*Service, *Autonomy, Delegation, string) {
	t.Helper()
	root := t.TempDir()
	s := newTestService(t, org, dec)
	ctx := context.Background()
	a := Defaults("p", "growth")
	a.Level = orgdesign.LevelFull
	a.Decider.Kind = orgdesign.DeciderBoss
	// tool:* is routine by default, which a rule would approve without ever
	// asking a decider — raise it so the fallback path is the one under test.
	a.Tiers = map[string]string{"tool:*": orgdesign.TierConsequential}
	if err := s.Store.Put(ctx, a, "cli"); err != nil {
		t.Fatal(err)
	}
	it := Item{Kind: KindApproval, Ref: "dev:WebFetch:1", Requester: "dev", Action: "WebFetch",
		Class: "tool:WebFetch", Tier: orgdesign.TierConsequential, Hash: "h"}
	d, err := s.Store.Delegate(ctx, "p", "growth", orgdesign.LevelFull, "growth", "lead", it, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	return s, a, *d, root
}

// pendingApproval is the same item as seen through monomind.
func pendingApproval() []map[string]interface{} {
	return []map[string]interface{}{{"roleId": "dev", "action": "WebFetch", "question": "q", "ts": 1, "approved": nil}}
}

// A source that fails makes Pending's list partial. Closing a delegation
// because its item is "gone" would strand it: ProcessOrg skips anything whose
// latest decision is an escalation at the current level, so nothing would ever
// decide it again.
func TestSweepKeepsDelegationWhenPendingIsPartial(t *testing.T) {
	org := &fakeOrg{approvals: pendingApproval(), approvalsErr: errors.New("monomind org approvals: timeout")}
	dec := &scriptedDecider{replies: map[string]string{KindApproval: `{"verdict":"approve","rationale":"ok"}`}}
	s, _, d, root := delegated(t, org, dec)
	ctx := context.Background()

	s.SweepDelegations(ctx, "p", root)

	got, err := s.Store.GetDelegation(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != DelegationPending {
		t.Fatalf("delegation status = %s, want it left pending while the list is partial", got.Status)
	}
	if len(dec.prompts) != 0 {
		t.Fatalf("fallback ran on a partial list: %d prompts", len(dec.prompts))
	}
}

// The pause brake stops the expiry fallback too: a paused org decides nothing,
// so the delegation waits for the pause to end instead of being auto-decided
// at the level captured when it was created.
func TestSweepRespectsPause(t *testing.T) {
	org := &fakeOrg{approvals: pendingApproval()}
	dec := &scriptedDecider{replies: map[string]string{KindApproval: `{"verdict":"approve","rationale":"ok"}`}}
	s, a, d, root := delegated(t, org, dec)
	ctx := context.Background()
	until := time.Now().Add(time.Hour)
	a.PausedUntil = &until
	if err := s.Store.Put(ctx, a, "cli"); err != nil {
		t.Fatal(err)
	}

	s.SweepDelegations(ctx, "p", root)

	if len(dec.prompts) != 0 || len(org.resolved) != 0 {
		t.Fatalf("paused org decided anyway: prompts=%d resolved=%v", len(dec.prompts), org.resolved)
	}
	got, _ := s.Store.GetDelegation(ctx, d.ID)
	if got.Status != DelegationPending {
		t.Fatalf("delegation status = %s, want pending while paused", got.Status)
	}

	// Once the pause ends, the same sweep falls back as before.
	a.PausedUntil = nil
	if err := s.Store.Put(ctx, a, "cli"); err != nil {
		t.Fatal(err)
	}
	s.SweepDelegations(ctx, "p", root)
	if len(dec.prompts) != 1 {
		t.Fatalf("fallback after the pause: %d prompts", len(dec.prompts))
	}
}
