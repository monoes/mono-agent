package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	httpnodes "github.com/monoes/mono-agent/internal/nodes/http"
	"github.com/monoes/mono-agent/internal/orgbridge"
	"github.com/monoes/mono-agent/internal/orgdesign"
	"github.com/monoes/mono-agent/internal/storage"
	"github.com/monoes/mono-agent/internal/tracesig"
	"github.com/monoes/mono-agent/internal/workflow"
)

// A signed trace that keeps coming back in through a webhook climbs a hop
// each time and is refused past the default limit: a workflow calling its
// own webhook is a loop like any other (U10).
func TestAdmitWebhookTraceStopsALoop(t *testing.T) {
	f := newOrgCLIFixture(t)
	db, err := storage.NewDatabase(f.cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := &orgServices{db: db, logf: t.Logf}
	ctx := context.Background()

	hop := 0
	for i := 1; i <= 8; i++ {
		got, refusal, err := s.admitWebhookTrace(ctx, f.plainWF, "chn_selfloop", hop)
		if err != nil || refusal != "" {
			t.Fatalf("crossing %d: hop %d, refusal %q, err %v", i, got, refusal, err)
		}
		if got != i {
			t.Fatalf("crossing %d admitted at hop %d, want %d", i, got, i)
		}
		hop = got
	}
	if _, refusal, err := s.admitWebhookTrace(ctx, f.plainWF, "chn_selfloop", hop); err != nil || !strings.Contains(refusal, "hop 9") {
		t.Fatalf("9th crossing: refusal %q, err %v — want refused at hop 9", refusal, err)
	}
	// A replayed hop-0 token is only one more run at hop 1: no more than an
	// unsigned request to the same webhook (a fresh chain at hop 0) gives.
	if got, refusal, _ := s.admitWebhookTrace(ctx, f.plainWF, "chn_selfloop", 0); refusal != "" || got != 1 {
		t.Fatalf("replayed hop-0 token: hop %d refusal %q, want a run at hop 1", got, refusal)
	}
}

// One run sending many requests to a local webhook (a fan-out over its
// items) is not a loop: every request carries the sender's hop, so each run
// it starts is at that hop + 1, however many there are, and none is refused
// by a repeat limit either.
func TestAdmitWebhookTraceAllowsAFanOut(t *testing.T) {
	f := newOrgCLIFixture(t)
	db, err := storage.NewDatabase(f.cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := &orgServices{db: db, logf: t.Logf}
	for i := 1; i <= 100; i++ {
		got, refusal, err := s.admitWebhookTrace(context.Background(), f.plainWF, "chn_fanout", 3)
		if err != nil || refusal != "" || got != 4 {
			t.Fatalf("request %d of a fan-out: hop %d, refusal %q, err %v — want admitted at 4", i, got, refusal, err)
		}
	}
}

// End to end in one process: a real http.request node posting to a real
// webhook server whose crossings go through the daemon's ledger. A run that
// posts to its own webhook is a loop and stops at the hop limit; a run that
// posts many items to a webhook once is a fan-out and all of them run.
func TestSignedTraceThroughARealWebhook(t *testing.T) {
	f := newOrgCLIFixture(t)
	db, err := storage.NewDatabase(f.cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := &orgServices{db: db, logf: t.Logf}

	hooks := workflow.NewWebhookServer("127.0.0.1:0", zerolog.Nop())
	hooks.SetTraceAdmitter(s.admitWebhookTrace)
	srv := httptest.NewServer(hooks)
	defer srv.Close()

	post := func(ctx context.Context, url string) {
		_, _ = (&httpnodes.RequestNode{}).Execute(ctx,
			workflow.NodeInput{Items: []workflow.Item{workflow.NewItem(map[string]interface{}{})}},
			map[string]interface{}{"method": "POST", "url": url, "body": "{}"})
	}

	// The loop: every run posts to its own webhook once.
	var loopRuns int
	if err := hooks.Register(&workflow.WebhookRegistration{WorkflowID: f.plainWF, Path: "self", Method: "POST",
		TriggerFn: func(items []workflow.Item) {
			loopRuns++
			if loopRuns > 50 {
				return // a safety net for the test itself, far past the limit
			}
			post(workflow.WithTrigger(context.Background(), workflow.TriggerNodeTypeWebhook, items[0].JSON), srv.URL+"/webhook/self")
		}}); err != nil {
		t.Fatal(err)
	}
	post(context.Background(), srv.URL+"/webhook/self")
	if loopRuns != 9 {
		t.Fatalf("self-loop ran %d times, want 9 (hop 0 plus 8 returns, the 9th refused)", loopRuns)
	}

	// The fan-out: one run posts 30 items to a webhook; all 30 run.
	var fanRuns int
	if err := hooks.Register(&workflow.WebhookRegistration{WorkflowID: f.outboundWF, Path: "fan", Method: "POST",
		TriggerFn: func([]workflow.Item) { fanRuns++ }}); err != nil {
		t.Fatal(err)
	}
	sender := workflow.WithTrigger(context.Background(), workflow.TriggerNodeTypeWebhook,
		map[string]interface{}{workflow.WebhookTraceKey: map[string]interface{}{"chain_id": "chn_fan", "hop": float64(2)}})
	for i := 0; i < 30; i++ {
		post(sender, srv.URL+"/webhook/fan")
	}
	if fanRuns != 30 {
		t.Fatalf("fan-out of 30 ran %d, want 30", fanRuns)
	}
}

// #132 item 3: replaying one signed token at a webhook starts at most
// orgbridge.TriggerRepeats runs a minute; the rest are refused with 429
// Too Many Requests, and the ledger gets one row for all of those refusals.
func TestReplayedTokenIsRateLimited(t *testing.T) {
	f := newOrgCLIFixture(t)
	db, err := storage.NewDatabase(f.cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := &orgServices{db: db, logf: t.Logf}
	hooks := workflow.NewWebhookServer("127.0.0.1:0", zerolog.Nop())
	hooks.SetTraceAdmitter(s.admitWebhookTrace)
	runs := 0
	if err := hooks.Register(&workflow.WebhookRegistration{WorkflowID: f.plainWF, Path: "replayed", Method: "POST",
		TriggerFn: func([]workflow.Item) { runs++ }}); err != nil {
		t.Fatal(err)
	}
	key, err := tracesig.Key()
	if err != nil {
		t.Fatal(err)
	}
	tok, _ := tracesig.Sign(key, "chn_replayed", 1)
	codes := map[int]int{}
	for i := 0; i < orgbridge.TriggerRepeats+25; i++ {
		req := httptest.NewRequest(http.MethodPost, "/webhook/replayed", strings.NewReader("{}"))
		req.Header.Set(tracesig.Header, tok)
		rec := httptest.NewRecorder()
		hooks.ServeHTTP(rec, req)
		codes[rec.Code]++
	}
	if runs != orgbridge.TriggerRepeats || codes[http.StatusOK] != orgbridge.TriggerRepeats || codes[http.StatusTooManyRequests] != 25 {
		t.Fatalf("runs %d, status codes %v — want %d runs and 25 × 429", runs, codes, orgbridge.TriggerRepeats)
	}
	var rows int
	if err := db.DB.QueryRow(`SELECT COUNT(*) FROM org_bridge_calls WHERE chain_id = 'chn_replayed'`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != orgbridge.TriggerRepeats+1 {
		t.Fatalf("%d ledger rows, want %d", rows, orgbridge.TriggerRepeats+1)
	}
}

// #132 item 5: a webhook_in crossing on a chain an org started is held to
// that org's run_config.max_hops, not the default of 8; a chain no org
// started keeps the default.
func TestAdmitWebhookTraceUsesTheChainOrgsMaxHops(t *testing.T) {
	f := newOrgCLIFixture(t)
	doc := f.load(t)
	if doc.RunConfig == nil {
		doc.RunConfig = map[string]json.RawMessage{}
	}
	doc.RunConfig["max_hops"] = json.RawMessage("3")
	if _, err := orgdesign.Save(f.root, doc); err != nil {
		t.Fatal(err)
	}
	db, err := storage.NewDatabase(f.cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := &orgServices{db: db, logf: t.Logf}
	ctx := context.Background()
	// growth sent the chain out: a workflow_out crossing it made.
	if _, err := orgbridge.NewLedger(db.DB).Admit(ctx, orgbridge.Call{ProfileID: "default",
		Trace: orgbridge.Trace{ChainID: "chn_growth"}, OriginOrg: "growth", Direction: orgbridge.DirWorkflowOut,
		OrgName: "growth", WorkflowID: f.outboundWF}, orgbridge.Limits{}); err != nil {
		t.Fatal(err)
	}
	if hop, refusal, err := s.admitWebhookTrace(ctx, f.plainWF, "chn_growth", 2); err != nil || refusal != "" || hop != 3 {
		t.Fatalf("hop 3: %d %q %v", hop, refusal, err)
	}
	if _, refusal, err := s.admitWebhookTrace(ctx, f.plainWF, "chn_growth", 3); err != nil || !strings.Contains(refusal, "limit of 3") {
		t.Fatalf("hop 4 under growth's max_hops 3: refusal %q, err %v", refusal, err)
	}
	if _, refusal, _ := s.admitWebhookTrace(ctx, f.plainWF, "chn_noorg", 3); refusal != "" {
		t.Fatalf("a chain no org started was refused at hop 4: %q", refusal)
	}
}
