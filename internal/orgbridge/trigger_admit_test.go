package orgbridge

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/orgdesign"
	"github.com/monoes/mono-agent/internal/workflow"
)

// toolEvent is a role's tool event as monomind writes it: the role's chain
// in its data.
func toolEvent(tool, chain string, hop int) Event {
	return Event{Org: "hq", Run: "run1", Type: "tool", From: "hq:dev", Tool: tool,
		Data: map[string]interface{}{"chain_id": chain, "hop": float64(hop)}}
}

func runTrace(t *testing.T, items []workflow.Item) Trace {
	t.Helper()
	chain, hop, ok := workflow.ParseTrace(items[0].JSON)
	if !ok {
		t.Fatalf("run has no trace: %v", items[0].JSON)
	}
	return Trace{ChainID: chain, Hop: hop}
}

func countRows(t *testing.T, s *TriggerSource, where string, args ...interface{}) int {
	t.Helper()
	var n int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM org_bridge_calls WHERE `+where, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A trigger.org run is admitted through the ledger as org_event: it is one
// hop past the event and recorded, and a run of an event with no chain
// gets a fresh one.
func TestTriggerOrgRunsAreAdmitted(t *testing.T) {
	s := NewTriggerSource(nil, newTestDB(t))
	ctx := context.Background()
	items, ok := s.eventItems(ctx, "p", "", "wf-t", toolEvent("mcp__monoagent__publish", "chn_role", 2))
	if !ok {
		t.Fatal("refused")
	}
	if tr := runTrace(t, items); tr != (Trace{ChainID: "chn_role", Hop: 3}) {
		t.Fatalf("run trace = %+v, want chn_role at 3", tr)
	}
	if n := countRows(t, s, `direction = 'org_event' AND chain_id = 'chn_role' AND workflow_id = 'wf-t' AND hop = 3`); n != 1 {
		t.Fatalf("org_event rows = %d, want 1", n)
	}
	items, ok = s.eventItems(ctx, "p", "", "wf-t", Event{Org: "hq", Type: "gate", Data: map[string]interface{}{"name": "x"}})
	if tr := runTrace(t, items); !ok || tr.ChainID == "" || tr.ChainID == "chn_role" || tr.Hop != 1 {
		t.Fatalf("gate event run trace = %+v (ok %v), want a fresh chain at 1", tr, ok)
	}
	if n := countRows(t, s, `direction = 'event_start' AND chain_id = ? AND workflow_id = 'wf-t'`, runTrace(t, items).ChainID); n != 1 {
		t.Fatalf("event_start rows for the fresh chain = %d, want 1", n)
	}
}

// #132 item 1: R → tool event → T (trigger.org) → webhook → back to R by a
// path the ledger does not record. Every round the event carries R's same
// hop, and the webhook_in hop is T's signed hop + 1; before T was admitted
// that was the same hop every round and the loop never stopped.
func TestTriggerOrgLoopThroughAWebhookClimbs(t *testing.T) {
	s := NewTriggerSource(nil, newTestDB(t))
	ledger := NewLedger(s.DB)
	ctx := context.Background()
	prev := 0
	for round := 1; ; round++ {
		if round > 20 {
			t.Fatal("the loop was never refused")
		}
		items, ok := s.eventItems(ctx, "p", "", "wf-t", toolEvent("monoagent__notify", "chn_loop", 1))
		if !ok {
			if prev > 8 || round < 4 {
				t.Fatalf("refused in round %d after hop %d", round, prev)
			}
			return
		}
		tr := runTrace(t, items)
		if tr.Hop <= prev {
			t.Fatalf("round %d: T at hop %d, not above the previous round's %d", round, tr.Hop, prev)
		}
		// T's HTTP request comes back in through a webhook, signed at T's hop.
		adm, err := ledger.Admit(ctx, Call{ProfileID: "p", Trace: tr, Direction: DirWebhookIn, WorkflowID: "wf-w"}, Limits{})
		if err != nil {
			t.Fatal(err)
		}
		if !adm.OK() {
			return
		}
		prev = adm.Trace.Hop
	}
}

// #124: a Bash tool event carries the role's chain, but it is continued only
// when the workflow itself already crossed on that chain. So a loop
// R → Bash event → T → org.send → R climbs and stops, while T's first run
// starts a fresh chain.
func TestTriggerOrgBashToolEventLoopStops(t *testing.T) {
	s := NewTriggerSource(nil, newTestDB(t))
	ledger := NewLedger(s.DB)
	ctx := context.Background()
	roleChain := Trace{ChainID: "chn_roleown", Hop: 0}
	for round := 1; ; round++ {
		if round > 20 {
			t.Fatal("the Bash tool event loop was never refused")
		}
		items, ok := s.eventItems(ctx, "p", "", "wf-t", toolEvent("Bash", roleChain.ChainID, roleChain.Hop))
		if !ok {
			return
		}
		tr := runTrace(t, items)
		if round == 1 && tr.ChainID == "chn_roleown" {
			t.Fatal("the first Bash event put T on the role's own chain")
		}
		if round > 1 && tr.ChainID != roleChain.ChainID {
			t.Fatalf("round %d: T started chain %s, want to continue %s", round, tr.ChainID, roleChain.ChainID)
		}
		// T sends to R: a workflow_out crossing, whose trace R's next tool
		// events carry.
		adm, err := ledger.Admit(ctx, Call{ProfileID: "p", Trace: tr, Direction: DirWorkflowOut, OrgName: "hq", RoleID: "dev", WorkflowID: "wf-t"}, Limits{MaxRepeats: 100})
		if err != nil {
			t.Fatal(err)
		}
		if !adm.OK() {
			return
		}
		roleChain = adm.Trace
	}
}

// An audit workflow on a busy role's tool events is not a loop: it is not
// put on the role's chain (Bash events), and its runs on a granted call's
// chain do not raise that chain's depth, so the role's own granted calls
// keep their hop. Here the granted call's event reaches the ledger before
// the call does, so the audit's org_event row is the chain's first: that
// still does not make the chain the audit's.
func TestTriggerOrgAuditWorkflowDoesNotClimbTheRolesChain(t *testing.T) {
	s := NewTriggerSource(nil, newTestDB(t))
	ledger := NewLedger(s.DB)
	ctx := context.Background()
	for i := 0; i < 100; i++ {
		for _, tool := range []string{"Bash", "monoagent__publish"} {
			items, ok := s.eventItems(ctx, "p", "", "wf-audit", toolEvent(tool, "chn_busy", 2))
			if !ok {
				t.Fatalf("event %d (%s) refused", i, tool)
			}
			// The audit forwards each event elsewhere, on the run's chain.
			tr := runTrace(t, items)
			if tool == "Bash" && tr.ChainID == "chn_busy" {
				t.Fatalf("event %d: a Bash event put the audit run on the role's chain", i)
			}
			if tool != "Bash" && tr.Hop != 3 {
				t.Fatalf("event %d: granted call's event run at hop %d, want 3", i, tr.Hop)
			}
		}
	}
	adm, err := ledger.Admit(ctx, Call{ProfileID: "p", Trace: Trace{ChainID: "chn_busy", Hop: 2}, Direction: DirRoleTool, OrgName: "hq", RoleID: "dev"}, Limits{})
	if err != nil || !adm.OK() || adm.Trace.Hop != 3 {
		t.Fatalf("role's granted call after 100 audit runs: %+v, %v — want admitted at 3", adm, err)
	}
}

// #132 item 5: the org's run_config.max_hops applies to its trigger.org
// runs, not the default of 8.
func TestTriggerOrgUsesTheOrgsMaxHops(t *testing.T) {
	root := t.TempDir()
	doc := orgdesign.NewOrg("hq", "ship", orgdesign.NewOrgOptions{})
	doc.RunConfig = map[string]json.RawMessage{"max_hops": json.RawMessage("3")}
	if _, err := orgdesign.Save(root, doc); err != nil {
		t.Fatal(err)
	}
	if lim := LimitsFor(root, "hq"); lim.MaxHops != 3 {
		t.Fatalf("LimitsFor = %+v", lim)
	}
	s := NewTriggerSource(nil, newTestDB(t))
	if _, ok := s.eventItems(context.Background(), "p", root, "wf-t", toolEvent("monoagent__x", "chn_deep", 2)); !ok {
		t.Fatal("hop 3 refused under max_hops 3")
	}
	if _, ok := s.eventItems(context.Background(), "p", root, "wf-t", toolEvent("monoagent__x", "chn_deep", 3)); ok {
		t.Fatal("hop 4 admitted under max_hops 3")
	}
}

// #132 item 3: webhook_in (and org_event) are limited to TriggerRepeats
// runs per workflow per window, and refusals past the first are not
// recorded, so replaying a token cannot grow the table without bound.
func TestLedgerTriggerRepeatLimitBoundsTheTable(t *testing.T) {
	db := newTestDB(t)
	l := NewLedger(db)
	clock := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return clock }
	ctx := context.Background()
	replay := Call{ProfileID: "p", Trace: Trace{ChainID: "chn_replay", Hop: 1}, Direction: DirWebhookIn, WorkflowID: "wf-hook"}
	rows := func() int {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM org_bridge_calls WHERE workflow_id = 'wf-hook'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for i := 0; i < TriggerRepeats; i++ {
		if adm, err := l.Admit(ctx, replay, Limits{MaxRepeats: 5}); err != nil || !adm.OK() {
			t.Fatalf("replay %d: %+v %v — the org's max_repeats must not apply", i, adm, err)
		}
	}
	for i := 0; i < 50; i++ {
		adm, err := l.Admit(ctx, replay, Limits{})
		if err != nil || adm.Status != StatusRefusedRepeat {
			t.Fatalf("replay past the limit: %+v %v", adm, err)
		}
	}
	if n := rows(); n != TriggerRepeats+1 {
		t.Fatalf("%d rows after %d replays, want %d (the admitted ones and one refusal)", n, TriggerRepeats+50, TriggerRepeats+1)
	}
	// A token past its chain's hop limit, replayed, is recorded once too.
	deep := replay
	deep.WorkflowID, deep.Trace.Hop = "wf-other", 8
	for i := 0; i < 20; i++ {
		if adm, _ := l.Admit(ctx, deep, Limits{}); adm.Status != StatusRefusedHops {
			t.Fatalf("hop 9: %+v", adm)
		}
	}
	var refused int
	_ = db.QueryRow(`SELECT COUNT(*) FROM org_bridge_calls WHERE workflow_id = 'wf-other'`).Scan(&refused)
	if refused != 1 {
		t.Fatalf("%d refused_hops rows for 20 replays, want 1", refused)
	}
	// The next window admits again.
	clock = clock.Add(2 * time.Minute)
	if adm, _ := l.Admit(ctx, replay, Limits{}); !adm.OK() {
		t.Fatalf("next window: %+v", adm)
	}
}
