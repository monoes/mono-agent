package orgbridge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account/accounttest"
	"github.com/monoes/mono-agent/internal/accountdoor/doortest"
	"github.com/monoes/mono-agent/internal/orggrant"
	"github.com/monoes/mono-agent/internal/workflow"
)

// The endpoint is mounted outside the HTTP API's own auth, so it is a door of its
// own: a locked account refuses a delivery before the endpoint is looked up (an
// unknown id gets the same 401, not 404) and before anything is recorded.
func TestReceiverRefusesDeliveriesWhileLocked(t *testing.T) {
	for _, c := range doortest.Modes {
		t.Run(c.Name, func(t *testing.T) {
			db := newTestDB(t)
			ctx := context.Background()
			if _, err := db.Exec(`INSERT INTO workflows (id, name, profile_id, is_active) VALUES ('wf-pub', 'Publish', 'p', 0)`); err != nil {
				t.Fatal(err)
			}
			ep, err := orggrant.NewStore(db).CreateEndpoint(ctx, "p", "growth", "bot", "wf-pub")
			if err != nil {
				t.Fatal(err)
			}
			rcv := &Receiver{DB: db, Store: workflow.NewSQLiteWorkflowStore(db), VerifyWindow: time.Minute,
				RootOf: func(string) string { return t.TempDir() },
				Mux:    NewMux(func(ctx context.Context, _, _, _ string, _ func([]byte)) error { <-ctx.Done(); return nil })}
			t.Cleanup(func() { // an accepted delivery starts the org's bus tail: end it with the test
				rcv.mu.Lock()
				defer rcv.mu.Unlock()
				for _, unsubscribe := range rcv.subs {
					unsubscribe()
				}
			})
			mux := http.NewServeMux()
			rcv.Register(mux)
			srv := httptest.NewServer(mux)
			t.Cleanup(srv.Close)
			accounttest.Install(t, c.Mode)

			d := EndpointDelivery{OrgName: "growth", Run: "run-1", From: "hq:ceo", To: "growth:bot", Subject: "post this", Body: "hello", MessageID: "msg-1"}
			code, out := postDelivery(t, srv, ep.ID, d)

			if !c.Refused {
				if code != http.StatusAccepted || out["accepted"] != true {
					t.Fatalf("delivery = %d %v, want 202 accepted", code, out)
				}
				return
			}
			acct, _ := out["account"].(map[string]interface{})
			if code != http.StatusUnauthorized || out["error"] != "login_required" || out["login_required"] != true ||
				acct["state"] != c.State || acct["reason"] != c.Reason || len(out) != 3 {
				t.Fatalf("delivery = %d %v, want 401 login_required (%s/%s)", code, out, c.State, c.Reason)
			}
			if code, out := postDelivery(t, srv, orggrant.NewEndpointID(), d); code != http.StatusUnauthorized || out["error"] != "login_required" {
				t.Errorf("unknown endpoint = %d %v, want the same 401: a locked receiver does not say which endpoints exist", code, out)
			}
			if len(rcv.pending) != 0 || len(rcv.subs) != 0 {
				t.Errorf("a refused delivery was recorded: %d pending, %d subscriptions", len(rcv.pending), len(rcv.subs))
			}
			var n int
			if err := db.QueryRow(`SELECT COUNT(*) FROM workflow_executions`).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != 0 {
				t.Errorf("%d executions were created for a refused delivery", n)
			}
		})
	}
}
