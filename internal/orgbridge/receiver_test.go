package orgbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/orggrant"
	"github.com/monoes/mono-agent/internal/workflow"
)

func postDelivery(t *testing.T, srv *httptest.Server, id string, d EndpointDelivery) (int, map[string]interface{}) {
	t.Helper()
	b, _ := json.Marshal(d)
	resp, err := http.Post(srv.URL+"/org-endpoint/"+id, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestReceiverVerifiesDispatchesAndReplies(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO workflows (id, name, profile_id, is_active) VALUES ('wf-pub', 'Publish', 'p', 0)`); err != nil {
		t.Fatal(err)
	}
	ep, err := orggrant.NewStore(db).CreateEndpoint(ctx, "p", "growth", "bot", "wf-pub")
	if err != nil {
		t.Fatal(err)
	}
	store := workflow.NewSQLiteWorkflowStore(db)
	rcv := &Receiver{DB: db, Store: store, VerifyWindow: 50 * time.Millisecond, RootOf: func(string) string { return t.TempDir() },
		Mux: NewMux(func(ctx context.Context, _, _, _ string, _ func([]byte)) error { <-ctx.Done(); return nil })}
	mux := http.NewServeMux()
	rcv.Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	d := EndpointDelivery{OrgName: "growth", Run: "run-1", From: "hq:ceo", To: "growth:bot", Subject: "post this", Body: "[trace chn_p hop=2]\nhello world", MessageID: "msg-1"}

	if code, _ := postDelivery(t, srv, orggrant.NewEndpointID(), d); code != http.StatusNotFound {
		t.Fatalf("unknown endpoint id: %d", code)
	}
	wrongOrg := d
	wrongOrg.OrgName = "other"
	if code, _ := postDelivery(t, srv, ep.ID, wrongOrg); code != http.StatusNotFound {
		t.Fatalf("org mismatch: %d", code)
	}
	noID := d
	noID.MessageID = ""
	if code, _ := postDelivery(t, srv, ep.ID, noID); code != http.StatusBadRequest {
		t.Fatalf("missing messageId: %d", code)
	}

	code, out := postDelivery(t, srv, ep.ID, d)
	if code != http.StatusAccepted || out["verified"] != false {
		t.Fatalf("first delivery: %d %v", code, out)
	}
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM workflow_executions`).Scan(&n)
	if n != 0 {
		t.Fatal("workflow started before the bus confirmed the message")
	}

	// The org's bus shows the message: the run starts, as an unowned
	// org_message execution carrying the next hop.
	rcv.onEvent("growth", Event{Type: "message", From: "hq:ceo", To: "bot", Subject: d.Subject, Msg: d.Body, Data: map[string]interface{}{"messageId": "msg-1"}})
	var execID, trigger, status, data string
	if err := db.QueryRow(`SELECT id, trigger_type, status, trigger_data FROM workflow_executions`).Scan(&execID, &trigger, &status, &data); err != nil {
		t.Fatalf("no execution: %v", err)
	}
	if trigger != workflow.TriggerTypeOrgMessage || status != "QUEUED" || !strings.Contains(data, `"hop":3`) || !strings.Contains(data, `"text":"hello world"`) {
		t.Fatalf("execution = %s %s %s", trigger, status, data)
	}
	if code, out := postDelivery(t, srv, ep.ID, d); code != http.StatusOK || out["duplicate"] != true {
		t.Fatalf("retry of a dispatched message: %d %v", code, out)
	}

	// A direct POST that never went through the org is refused after the
	// window (C-4).
	forged := d
	forged.MessageID = "msg-forged"
	if code, _ := postDelivery(t, srv, ep.ID, forged); code != http.StatusAccepted {
		t.Fatalf("forged: %d", code)
	}
	time.Sleep(80 * time.Millisecond)

	var sent monomind.InboxMessage
	var sentOrg string
	inboxFunc = func(_ context.Context, _, name string, msg monomind.InboxMessage) (*monomind.InboxReceipt, error) {
		sent, sentOrg = msg, name
		return &monomind.InboxReceipt{V: 1, Org: name, Delivery: "live"}, nil
	}
	t.Cleanup(func() { inboxFunc = monomind.OrgInbox })

	// Finish the run with an output, then sweep: forged refused, reply sent.
	now := time.Now().UTC()
	if _, err := db.Exec(`UPDATE workflow_executions SET status = 'SUCCESS' WHERE id = ?`, execID); err != nil {
		t.Fatal(err)
	}
	en := &workflow.WorkflowExecutionNode{ExecutionID: execID, NodeID: "n1", NodeName: "publish", Status: "SUCCESS",
		OutputItems: []workflow.Item{{JSON: map[string]interface{}{"url": "https://example.test/post/1", "api_key": "secret-value"}}},
		StartedAt:   &now, FinishedAt: &now}
	if err := store.CreateExecutionNode(ctx, en); err != nil {
		t.Fatal(err)
	}
	rcv.Sweep(ctx)

	var refused int
	_ = db.QueryRow(`SELECT COUNT(*) FROM org_bridge_calls WHERE status = 'refused_grant' AND endpoint_id = ?`, ep.ID).Scan(&refused)
	if refused != 1 {
		t.Fatalf("forged delivery refusals = %d", refused)
	}
	if sentOrg != "hq" || sent.To != "ceo" || sent.From != "growth:bot" || sent.Subject != "re: post this" {
		t.Fatalf("reply = org %q %+v", sentOrg, sent)
	}
	if !strings.HasPrefix(sent.Body, "[trace chn_p hop=4]\n") || !strings.Contains(sent.Body, "example.test/post/1") || strings.Contains(sent.Body, "secret-value") {
		t.Fatalf("reply body = %q", sent.Body)
	}
	sent = monomind.InboxMessage{}
	rcv.Sweep(ctx)
	if sent.From != "" {
		t.Fatal("replied twice")
	}
}

func TestReceiverAnswersOrgAsk(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	seedWaitingExecution(t, db, "exec-ask")
	ep, err := orggrant.NewStore(db).CreateEndpoint(ctx, "p", "growth", "bot", "wf")
	if err != nil {
		t.Fatal(err)
	}
	ask := Ask{ID: NewAskID(), ProfileID: "p", OrgName: "growth", EndpointRoleID: "bot", ExecutionID: "exec-ask", NodeID: "n", DeadlineAt: time.Now().Add(time.Hour)}
	if err := NewAskStore(db).Create(ctx, ask); err != nil {
		t.Fatal(err)
	}
	var resumed string
	rcv := &Receiver{DB: db, Store: workflow.NewSQLiteWorkflowStore(db), Resume: func(id string) error { resumed = id; return nil }}
	rcv.dispatch(ctx, *ep, EndpointDelivery{OrgName: "growth", From: "lead", To: "bot", Subject: "re: ask:" + ask.ID, Body: "42", MessageID: "m"})
	got, _ := NewAskStore(db).Get(ctx, ask.ID)
	if got.Status != AskReplied || resumed != "exec-ask" {
		t.Fatalf("ask = %+v resumed=%q", got, resumed)
	}
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM workflow_executions WHERE trigger_type = ?`, workflow.TriggerTypeOrgMessage).Scan(&n)
	if n != 0 {
		t.Fatal("an ask reply started a new automation run")
	}
}
