package orgbridge

import (
	"context"
	"database/sql"
	"sync"
	"time"
)

// Waker resumes paused executions the moment the org event they wait for
// arrives, so org.run and org.ask pauses need no 3-second polling (C-33):
//   - an org.ask gets its reply stored and its execution resumed when a
//     message addressed to its automation role carries "ask:<id>";
//   - an org.run (or any node that crossed into an org) is resumed on every
//     org-level status event (run started, stopped, completed) and re-checks
//     the org itself;
//   - an ask past its deadline is expired and resumed so the node fails
//     promptly.
type Waker struct {
	DB       *sql.DB
	Mux      *Mux
	Resume   func(executionID string) error
	RootOf   func(profileID string) string
	Interval time.Duration
	Logf     func(format string, args ...interface{})

	mu   sync.Mutex
	subs map[tailKey]func()
}

// Run polls for waiting executions until ctx ends.
func (w *Waker) Run(ctx context.Context) {
	if w.Interval <= 0 {
		w.Interval = 10 * time.Second
	}
	if w.Logf == nil {
		w.Logf = func(string, ...interface{}) {}
	}
	w.subs = map[tailKey]func(){}
	t := time.NewTicker(w.Interval)
	defer t.Stop()
	defer w.unsubscribeAll()
	for {
		w.Sync(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (w *Waker) unsubscribeAll() {
	w.mu.Lock()
	defer w.mu.Unlock()
	for k, u := range w.subs {
		u()
		delete(w.subs, k)
	}
}

type watched struct {
	profileID string
	org       string
}

// Sync expires overdue asks and aligns tail subscriptions with the orgs
// that waiting executions depend on.
func (w *Waker) Sync(ctx context.Context) {
	if w.subs == nil {
		w.subs = map[tailKey]func(){}
	}
	need := map[watched]bool{}
	asks := NewAskStore(w.DB)
	waiting, err := asks.ListWaiting(ctx)
	if err != nil {
		w.Logf("orgbridge: waker: %v", err)
		return
	}
	for _, a := range waiting {
		if time.Now().After(a.DeadlineAt) {
			if ok, _ := asks.MarkTimedOut(ctx, a.ID); ok {
				w.resume(a.ExecutionID)
			}
			continue
		}
		need[watched{a.ProfileID, a.OrgName}] = true
	}
	rows, err := w.DB.QueryContext(ctx,
		`SELECT DISTINCT c.profile_id, c.org_name FROM org_bridge_calls c
		 JOIN workflow_executions e ON e.id = c.execution_id
		 WHERE e.status = 'WAITING' AND c.direction = ? AND c.status = 'ok' AND c.org_name IS NOT NULL`, DirWorkflowOut)
	if err != nil {
		w.Logf("orgbridge: waker: %v", err)
		return
	}
	for rows.Next() {
		var p, o string
		if rows.Scan(&p, &o) == nil {
			need[watched{p, o}] = true
		}
	}
	rows.Close()

	w.mu.Lock()
	defer w.mu.Unlock()
	wantKeys := map[tailKey]watched{}
	for n := range need {
		wantKeys[tailKey{w.RootOf(n.profileID), n.org}] = n
	}
	for k, u := range w.subs {
		if _, ok := wantKeys[k]; !ok {
			u()
			delete(w.subs, k)
		}
	}
	for k, n := range wantKeys {
		if _, ok := w.subs[k]; ok {
			continue
		}
		n := n
		w.subs[k] = w.Mux.Subscribe(k.root, k.org, func(ev Event) { w.handle(context.Background(), n, ev) })
	}
}

func (w *Waker) resume(executionID string) {
	if err := w.Resume(executionID); err != nil {
		w.Logf("orgbridge: waker: resume %s: %v", executionID, err)
	}
}

// Handle processes one event for an org (exported for tests).
func (w *Waker) Handle(ctx context.Context, profileID, org string, ev Event) {
	w.handle(ctx, watched{profileID, org}, ev)
}

func (w *Waker) handle(ctx context.Context, n watched, ev Event) {
	switch ev.Type {
	case "message", "xorg":
		ref := AskRef(ev.Subject, ev.Msg)
		if ref == "" {
			return
		}
		asks := NewAskStore(w.DB)
		a, err := asks.Get(ctx, ref)
		if err != nil || a == nil || a.Status != AskWaiting || a.ProfileID != n.profileID || a.OrgName != n.org {
			return
		}
		if ev.To != a.EndpointRoleID && ev.To != a.OrgName+":"+a.EndpointRoleID {
			return
		}
		reply := map[string]interface{}{"from": ev.From, "subject": ev.Subject, "body": StripTrace(ev.Msg)}
		if tr, ok := ParseTrace(ev.Msg); ok {
			reply["trace"] = map[string]interface{}{"chain_id": tr.ChainID, "hop": tr.Hop}
		}
		if ok, _ := asks.MarkReplied(ctx, a.ID, reply); ok {
			w.resume(a.ExecutionID)
		}
	case "status":
		if ev.From != "" { // role-level status; only org-level changes matter
			return
		}
		rows, err := w.DB.QueryContext(ctx,
			`SELECT DISTINCT e.id FROM workflow_executions e JOIN org_bridge_calls c ON c.execution_id = e.id
			 WHERE e.status = 'WAITING' AND c.direction = ? AND c.status = 'ok' AND c.profile_id = ? AND c.org_name = ?`,
			DirWorkflowOut, n.profileID, n.org)
		if err != nil {
			return
		}
		var ids []string
		for rows.Next() {
			var id string
			if rows.Scan(&id) == nil {
				ids = append(ids, id)
			}
		}
		rows.Close()
		for _, id := range ids {
			w.resume(id)
		}
	}
}
