package orgbridge

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/storage"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := storage.NewDatabase(filepath.Join(t.TempDir(), "b.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.ApplyMigrations(); err != nil {
		t.Fatal(err)
	}
	return db.DB
}

func TestTraceParseAndReplace(t *testing.T) {
	body := "hello\n[trace chn_abc_1 hop=3]\nrest"
	tr, ok := ParseTrace(body)
	if !ok || tr.ChainID != "chn_abc_1" || tr.Hop != 3 {
		t.Fatalf("ParseTrace = %+v, %v", tr, ok)
	}
	out := WithTrace(body, Trace{ChainID: "chn_x", Hop: 4})
	if !strings.HasPrefix(out, "[trace chn_x hop=4]\n") || strings.Count(out, "[trace") != 1 {
		t.Fatalf("WithTrace = %q", out)
	}
	if _, ok := ParseTrace("[trace nope hop=1]"); ok {
		t.Fatal("malformed chain id accepted")
	}
	if StripTrace(out) != "hello\n\nrest" {
		t.Fatalf("StripTrace = %q", StripTrace(out))
	}
}

// A forged low hop in the header cannot reset the count; the chain stops at
// MaxHops (U10 trust rule).
func TestLedgerHopsNeverLowered(t *testing.T) {
	l := NewLedger(newTestDB(t))
	ctx := context.Background()
	lim := Limits{MaxHops: 3, MaxRepeats: 100}
	c := Call{ProfileID: "p", Direction: DirWorkflowOut, OrgName: "hq", Trace: Trace{ChainID: "chn_loop"}}
	for i := 1; i <= 3; i++ {
		c.Trace.Hop = 0 // forged: always claims hop 0
		adm, err := l.Admit(ctx, c, lim)
		if err != nil {
			t.Fatal(err)
		}
		if !adm.OK() || adm.Trace.Hop != i {
			t.Fatalf("call %d: %+v", i, adm)
		}
	}
	adm, err := l.Admit(ctx, c, lim)
	if err != nil {
		t.Fatal(err)
	}
	if adm.Status != StatusRefusedHops {
		t.Fatalf("4th hop admitted: %+v", adm)
	}
}

// Fresh chain ids every call still hit the per-target repeat limit.
func TestLedgerRepeatLimitIgnoresChain(t *testing.T) {
	l := NewLedger(newTestDB(t))
	ctx := context.Background()
	lim := Limits{MaxRepeats: 2, Window: time.Minute}
	target := Call{ProfileID: "p", Direction: DirRoleTool, OrgName: "g", RoleID: "lead", WorkflowID: "wf"}
	for i := 0; i < 2; i++ {
		if adm, _ := l.Admit(ctx, target, lim); !adm.OK() {
			t.Fatalf("call %d refused: %+v", i, adm)
		}
	}
	adm, _ := l.Admit(ctx, target, lim)
	if adm.Status != StatusRefusedRepeat {
		t.Fatalf("third call to the same target admitted: %+v", adm)
	}
	other := target
	other.WorkflowID = "wf2"
	if adm, _ := l.Admit(ctx, other, lim); !adm.OK() {
		t.Fatalf("different target refused: %+v", adm)
	}
}

func TestSendWritesTraceAndReportsDelivery(t *testing.T) {
	l := NewLedger(newTestDB(t))
	var got monomind.InboxMessage
	inboxFunc = func(ctx context.Context, root, name string, msg monomind.InboxMessage) (*monomind.InboxReceipt, error) {
		got = msg
		return &monomind.InboxReceipt{V: 1, Org: name, To: msg.To, From: msg.From, Delivery: "live", MessageID: "msg-1"}, nil
	}
	t.Cleanup(func() { inboxFunc = monomind.OrgInbox })

	res, err := Send(context.Background(), l, SendRequest{
		ProfileID: "p", Root: "/r", Org: "growth", To: "lead", From: "workflow:abc", Subject: "hi", Body: "body",
		Trace: Trace{ChainID: "chn_in", Hop: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Delivery != "live" || res.Trace.Hop != 3 || !strings.HasPrefix(got.Body, "[trace chn_in hop=3]\nbody") {
		t.Fatalf("res=%+v body=%q", res, got.Body)
	}

	inboxFunc = func(context.Context, string, string, monomind.InboxMessage) (*monomind.InboxReceipt, error) {
		return nil, errors.New("boom")
	}
	if _, err := Send(context.Background(), l, SendRequest{ProfileID: "p", Org: "growth", From: "workflow:abc"}); err == nil {
		t.Fatal("delivery error swallowed")
	}
}

func TestMuxSharesOneTailAndDedupes(t *testing.T) {
	var mu sync.Mutex
	starts := 0
	lines := make(chan []byte, 10)
	m := NewMux(func(ctx context.Context, root, org, since string, onLine func([]byte)) error {
		mu.Lock()
		starts++
		mu.Unlock()
		for {
			select {
			case <-ctx.Done():
				return nil
			case l := <-lines:
				onLine(l)
			}
		}
	})
	var got1, got2 []string
	var gm sync.Mutex
	u1 := m.Subscribe("/r", "hq", func(ev Event) { gm.Lock(); got1 = append(got1, ev.ID); gm.Unlock() })
	u2 := m.Subscribe("/r", "hq", func(ev Event) { gm.Lock(); got2 = append(got2, ev.ID); gm.Unlock() })
	lines <- []byte(`{"id":"e1","type":"status","org":"hq"}`)
	deadline := time.Now().Add(2 * time.Second)
	for {
		gm.Lock()
		done := len(got1) == 1 && len(got2) == 1
		gm.Unlock()
		if done || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	if starts != 1 {
		t.Fatalf("tail started %d times for two subscribers", starts)
	}
	mu.Unlock()
	if len(got1) != 1 || len(got2) != 1 {
		t.Fatalf("got1=%v got2=%v", got1, got2)
	}
	u1()
	if m.Subscribers("/r", "hq") != 1 {
		t.Fatal("unsubscribe did not remove one subscriber")
	}
	u2()
	if m.Subscribers("/r", "hq") != 0 {
		t.Fatal("tail kept after last unsubscribe")
	}

	d := NewDeduper()
	a := Event{Type: "xorg", From: "herald:editor", To: "forge:cto", Subject: "s", Msg: "m"}
	if !d.FirstSighting(a) || d.FirstSighting(a) {
		t.Fatal("content dedupe failed")
	}
	b := Event{Type: "xorg", Data: map[string]interface{}{"messageId": "msg-9"}, Subject: "x"}
	c := Event{Type: "xorg", Data: map[string]interface{}{"messageId": "msg-9"}, Subject: "y"}
	if !d.FirstSighting(b) || d.FirstSighting(c) {
		t.Fatal("messageId dedupe failed")
	}
}

// Real recorded buses parse and classify question kinds (C-45).
func TestParseRecordedBusEvents(t *testing.T) {
	approval, err := ParseEvent([]byte(`{"id":"1","type":"question","from":"dev","data":{"question":"Approval required for Bash","action":"Bash"}}`))
	if err != nil || approval.QuestionKind() != "approval" {
		t.Fatalf("approval kind = %q (%v)", approval.QuestionKind(), err)
	}
	ask, _ := ParseEvent([]byte(`{"id":"2","type":"question","from":"dev","data":{"questionId":"q-1","question":"Which?"}}`))
	if ask.QuestionKind() != "ask_human" {
		t.Fatalf("ask kind = %q", ask.QuestionKind())
	}
}

// TestMuxSamplesResumeCursorBeforeStartingTheTail is a regression test: the
// tail sampled its `since` cursor inside the spawned goroutine, so an event
// the caller causes right after Subscribe returns — monomind emits the
// confirming bus event immediately after its 202 — could be stamped before
// the cursor and skipped as "older than the tail". The endpoint delivery
// then sat pending for the whole VerifyWindow and Sweep refused it as
// refused_grant, so the automation never ran and no reply was sent.
func TestMuxSamplesResumeCursorBeforeStartingTheTail(t *testing.T) {
	t0 := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	release := make(chan struct{})
	var clockMu sync.Mutex
	clock := t0
	since := make(chan string, 1)

	m := NewMux(func(ctx context.Context, _, _, s string, _ func([]byte)) error {
		since <- s
		<-ctx.Done()
		return nil
	})
	// now() reads the clock and then parks until the test releases it, so a
	// cursor sampled inside Subscribe parks Subscribe itself.
	m.now = func() time.Time {
		clockMu.Lock()
		v := clock
		clockMu.Unlock()
		<-release
		return v
	}

	subscribed := make(chan func(), 1)
	go func() { subscribed <- m.Subscribe("/root", "growth", func(Event) {}) }()

	select {
	case <-subscribed:
		t.Fatal("Subscribe returned without sampling the tail's resume cursor: it is sampled inside the goroutine, " +
			"so an event the caller causes right after Subscribe returns is skipped as older than the tail")
	case <-time.After(100 * time.Millisecond):
	}

	// Time moves on before the tail goroutine runs; the cursor must still
	// be the one taken while Subscribe was running.
	clockMu.Lock()
	clock = t0.Add(time.Hour)
	clockMu.Unlock()
	close(release)

	unsub := <-subscribed
	defer unsub()
	select {
	case got := <-since:
		if want := t0.Format(time.RFC3339Nano); got != want {
			t.Fatalf("tail resumed from %q, want %q", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the tail never started")
	}
}

// run_config.max_hops / max_repeats reach Admit straight from the org JSON,
// which any role whose fileWrite reaches .monomind/ can edit (C-3), so a
// role could otherwise disarm U10 by raising its own limits.
func TestLedgerClampsOrgSuppliedLimits(t *testing.T) {
	huge := Limits{MaxHops: 1 << 30, MaxRepeats: 1 << 30}
	if got := huge.withDefaults(); got.MaxHops != MaxHopsCeiling || got.MaxRepeats != MaxRepeatsCeiling {
		t.Fatalf("withDefaults = %+v, want MaxHops %d and MaxRepeats %d", got, MaxHopsCeiling, MaxRepeatsCeiling)
	}

	db := newTestDB(t)
	ctx := context.Background()
	l := NewLedger(db)
	tr := NewTrace()
	var last Admission
	for i := 0; i <= MaxHopsCeiling; i++ {
		a, err := l.Admit(ctx, Call{ProfileID: "p", Trace: tr, Direction: DirWorkflowOut, OrgName: "o", RoleID: "r"}, huge)
		if err != nil {
			t.Fatal(err)
		}
		last = a
	}
	if last.Status != StatusRefusedHops {
		t.Fatalf("hop %d admitted as %q — an org that raises max_hops is never stopped", last.Trace.Hop, last.Status)
	}
}

// queryPlan returns the EXPLAIN QUERY PLAN detail lines for q.
func queryPlan(t *testing.T, db *sql.DB, q string, args ...interface{}) string {
	t.Helper()
	rows, err := db.Query("EXPLAIN QUERY PLAN "+q, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for rows.Next() {
		cells := make([]interface{}, len(cols))
		for i := range cells {
			cells[i] = new(sql.NullString)
		}
		if err := rows.Scan(cells...); err != nil {
			t.Fatal(err)
		}
		out = append(out, cells[len(cells)-1].(*sql.NullString).String)
	}
	return strings.Join(out, "\n")
}

// org_bridge_calls is an append-only audit log with no pruning, and org.run
// asks HasCrossing on every execution — including every 30s resume-poll
// wake — so its execution_id lookup must not scan the whole table.
func TestBridgeCallsExecutionLookupUsesAnIndex(t *testing.T) {
	db := newTestDB(t)
	for q, args := range map[string][]interface{}{
		`SELECT COUNT(*) FROM org_bridge_calls WHERE execution_id = ? AND direction = ? AND org_name = ? AND status = 'ok'`: {"exec-1", DirWorkflowOut, "growth"},
		`SELECT 1 FROM org_bridge_calls x WHERE x.execution_id = ? AND x.direction = ?`:                                     {"exec-1", DirEndpointReply},
	} {
		plan := queryPlan(t, db, q, args...)
		if strings.Contains(plan, "SCAN") {
			t.Errorf("%s\nplans as a table scan:\n%s", q, plan)
		}
	}
}

// A role's granted calls in reply to one message are siblings: they share a
// hop, so a busy role is not refused as a loop after max_hops calls.
func TestLedgerSiblingRoleCallsShareAHop(t *testing.T) {
	l := NewLedger(newTestDB(t))
	ctx := context.Background()
	lim := Limits{MaxHops: 3, MaxRepeats: 100}
	c := Call{ProfileID: "p", Direction: DirRoleTool, OrgName: "g", RoleID: "writer", WorkflowID: "wf", Trace: Trace{ChainID: "chn_task"}}
	for i := 0; i < 10; i++ {
		adm, err := l.Admit(ctx, c, lim)
		if err != nil {
			t.Fatal(err)
		}
		if !adm.OK() || adm.Trace.Hop != 1 {
			t.Fatalf("sibling call %d: %+v", i+1, adm)
		}
	}
	// Another role in the same chain is not this role's sibling.
	other := c
	other.RoleID = "editor"
	if adm, _ := l.Admit(ctx, other, lim); adm.Trace.Hop != 2 {
		t.Fatalf("another role's call: %+v, want hop 2", adm)
	}
}

// A loop through a granted automation still climbs and stops: the workflow's
// message back into the org is recorded under its own crossing, so the
// role's next call is a hop further even though its own calls are left out.
func TestLedgerRoleToolLoopStillStops(t *testing.T) {
	l := NewLedger(newTestDB(t))
	ctx := context.Background()
	lim := Limits{MaxHops: 6, MaxRepeats: 100}
	role := Call{ProfileID: "p", Direction: DirRoleTool, OrgName: "g", RoleID: "writer", WorkflowID: "wf", Trace: Trace{ChainID: "chn_loop2"}}
	back := Call{ProfileID: "p", Direction: DirWorkflowOut, OrgName: "g", RoleID: "writer", WorkflowID: "wf", Trace: Trace{ChainID: "chn_loop2"}}
	var last Admission
	for i := 0; i < 10; i++ {
		role.Trace.Hop = 0 // forged low every time
		a, err := l.Admit(ctx, role, lim)
		if err != nil {
			t.Fatal(err)
		}
		last = a
		if !a.OK() {
			break
		}
		back.Trace = a.Trace
		if b, err := l.Admit(ctx, back, lim); err != nil {
			t.Fatal(err)
		} else if !b.OK() {
			last = b
			break
		}
	}
	if last.Status != StatusRefusedHops {
		t.Fatalf("role → automation → org loop never refused: %+v", last)
	}
}
