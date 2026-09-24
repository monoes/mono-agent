package httpnodes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/monoes/mono-agent/internal/tracesig"
	"github.com/monoes/mono-agent/internal/workflow"
)

// sentTraceHeader runs one GET in a run started by triggerType with data,
// and returns the X-Monoagent-Trace header the server saw.
func sentTraceHeader(t *testing.T, triggerType string, data map[string]interface{}, headers map[string]interface{}) string {
	t.Helper()
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get(tracesig.Header)
		w.WriteHeader(200)
	}))
	defer srv.Close()
	ctx := workflow.WithTrigger(context.Background(), triggerType, data)
	cfg := map[string]interface{}{"method": "GET", "url": srv.URL}
	if headers != nil {
		cfg["headers"] = headers
	}
	if _, err := (&RequestNode{}).Execute(ctx, workflow.NodeInput{Items: []workflow.Item{workflow.NewItem(map[string]interface{}{})}}, cfg); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	return got
}

// A run on a chain sends it on, signed with this machine's key, so a
// webhook it reaches can continue the chain; a run on no chain sends none.
func TestRequestNodeSignsTheRunsChain(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	key, err := tracesig.Key()
	if err != nil {
		t.Fatal(err)
	}
	onChain := map[string]interface{}{"trace": map[string]interface{}{"chain_id": "chn_run", "hop": float64(2)}}

	tok := sentTraceHeader(t, workflow.TriggerTypeOrgTool, onChain, nil)
	if chain, hop, ok := tracesig.Verify(key, tok); !ok || chain != "chn_run" || hop != 2 {
		t.Fatalf("header %q verifies as %q %d %v, want chn_run at 2", tok, chain, hop, ok)
	}

	webhookRun := map[string]interface{}{workflow.WebhookTraceKey: map[string]interface{}{"chain_id": "chn_hook", "hop": float64(5)}}
	if chain, hop, ok := tracesig.Verify(key, sentTraceHeader(t, workflow.TriggerNodeTypeWebhook, webhookRun, nil)); !ok || chain != "chn_hook" || hop != 5 {
		t.Fatalf("webhook run header: %q %d %v", chain, hop, ok)
	}

	if tok := sentTraceHeader(t, "trigger.manual", onChain, nil); tok != "" {
		t.Fatalf("a manual run sent a trace header %q", tok)
	}
}

// A workflow cannot send a trace of its own choosing: a configured header
// of that name is replaced by the run's own, or removed when there is none
// (a replayed token copied from input must not travel).
func TestRequestNodeTraceHeaderCannotBeSetByTheWorkflow(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	key, _ := tracesig.Key()
	replayed, _ := tracesig.Sign(key, "chn_victim", 0)
	custom := map[string]interface{}{tracesig.Header: replayed}

	if tok := sentTraceHeader(t, "trigger.manual", map[string]interface{}{}, custom); tok != "" {
		t.Fatalf("configured header travelled from a run on no chain: %q", tok)
	}
	onChain := map[string]interface{}{"trace": map[string]interface{}{"chain_id": "chn_run", "hop": float64(1)}}
	if chain, _, _ := tracesig.Verify(key, sentTraceHeader(t, workflow.TriggerTypeOrgTool, onChain, custom)); chain != "chn_run" {
		t.Fatalf("configured header won over the run's chain: %q", chain)
	}
}
