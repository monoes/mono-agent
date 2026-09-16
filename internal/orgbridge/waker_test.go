package orgbridge

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"
)

func seedWaitingExecution(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	if _, err := db.Exec(`INSERT OR IGNORE INTO workflows (id, name, profile_id) VALUES ('wf', 'wf', 'p')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO workflow_executions (id, workflow_id, status, trigger_type, profile_id) VALUES (?, 'wf', 'WAITING', 'trigger.manual', 'p')`, id); err != nil {
		t.Fatal(err)
	}
}

func TestWakerRepliesAsksAndWakesRuns(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	var mu sync.Mutex
	var resumed []string
	w := &Waker{DB: db, Mux: NewMux(func(ctx context.Context, _, _, _ string, _ func([]byte)) error { <-ctx.Done(); return nil }),
		Resume: func(id string) error { mu.Lock(); resumed = append(resumed, id); mu.Unlock(); return nil },
		RootOf: func(string) string { return "/r" }}

	seedWaitingExecution(t, db, "exec-ask")
	seedWaitingExecution(t, db, "exec-run")
	seedWaitingExecution(t, db, "exec-late")
	asks := NewAskStore(db)
	ask := Ask{ID: NewAskID(), ProfileID: "p", OrgName: "growth", EndpointRoleID: "bot", ExecutionID: "exec-ask", NodeID: "n", DeadlineAt: time.Now().Add(time.Hour)}
	if err := asks.Create(ctx, ask); err != nil {
		t.Fatal(err)
	}
	late := Ask{ID: NewAskID(), ProfileID: "p", OrgName: "growth", EndpointRoleID: "bot", ExecutionID: "exec-late", NodeID: "n", DeadlineAt: time.Now().Add(-time.Second)}
	if err := asks.Create(ctx, late); err != nil {
		t.Fatal(err)
	}
	if err := NewLedger(db).insert(ctx, "c1", Call{ProfileID: "p", Direction: DirWorkflowOut, OrgName: "growth", ExecutionID: "exec-run"}, Trace{ChainID: "chn_x", Hop: 1}, StatusOK); err != nil {
		t.Fatal(err)
	}

	w.Sync(ctx)
	if got, _ := asks.Get(ctx, late.ID); got.Status != AskTimedOut {
		t.Fatalf("overdue ask status = %s", got.Status)
	}
	if w.Mux.Subscribers("/r", "growth") != 1 {
		t.Fatalf("subscribers = %d, want one shared tail", w.Mux.Subscribers("/r", "growth"))
	}

	// A reply to the wrong role is ignored; the right one stores the reply.
	w.Handle(ctx, "p", "growth", Event{Type: "message", From: "lead", To: "other", Subject: "re: ask:" + ask.ID})
	w.Handle(ctx, "p", "growth", Event{Type: "message", From: "lead", To: "bot", Subject: "re: ask:" + ask.ID + " hi", Msg: "[trace chn_q hop=3]\nthe answer"})
	got, _ := asks.Get(ctx, ask.ID)
	if got.Status != AskReplied || got.Reply["body"] != "the answer" {
		t.Fatalf("ask after reply = %+v", got)
	}

	w.Handle(ctx, "p", "growth", Event{Type: "status", From: "dev", Msg: "working"})
	w.Handle(ctx, "p", "growth", Event{Type: "status", Msg: "org stopped"})

	mu.Lock()
	defer mu.Unlock()
	want := map[string]bool{"exec-late": true, "exec-ask": true, "exec-run": true}
	for _, id := range resumed {
		delete(want, id)
	}
	if len(want) != 0 {
		t.Fatalf("not resumed: %v (resumed %v)", want, resumed)
	}

	// Once nothing waits, the tail is released.
	if _, err := db.Exec(`UPDATE workflow_executions SET status = 'SUCCESS'`); err != nil {
		t.Fatal(err)
	}
	w.Sync(ctx)
	if w.Mux.Subscribers("/r", "growth") != 0 {
		t.Fatal("tail kept with nothing waiting")
	}
}
