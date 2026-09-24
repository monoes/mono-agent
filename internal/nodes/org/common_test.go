package org

import (
	"context"
	"testing"

	"github.com/monoes/mono-agent/internal/workflow"
)

func traced(chain string, hop interface{}) map[string]interface{} {
	return map[string]interface{}{"trace": map[string]interface{}{"chain_id": chain, "hop": hop}}
}

// A node between the trigger and org.send may drop the `trace` field; the
// chain then continues from the execution's trigger data rather than
// starting over, which would reset the hop count of a loop.
func TestIncomingTraceFallsBackToTriggerData(t *testing.T) {
	for _, tt := range []string{workflow.TriggerTypeOrgTool, workflow.TriggerTypeOrgMessage, workflow.TriggerNodeTypeOrg} {
		ctx := workflow.WithTrigger(context.Background(), tt, traced("chn_start", float64(4)))

		if tr := incomingTrace(ctx, workflow.Item{JSON: map[string]interface{}{"other": 1}}); tr.ChainID != "chn_start" || tr.Hop != 4 {
			t.Fatalf("%s: fallback trace = %+v", tt, tr)
		}
		// A later hop on the same chain (a previous org node's output) wins.
		if tr := incomingTrace(ctx, workflow.Item{JSON: traced("chn_start", 7)}); tr.ChainID != "chn_start" || tr.Hop != 7 {
			t.Fatalf("%s: same-chain item trace = %+v, want hop 7", tt, tr)
		}
		// Another chain on the item is outside data and is ignored.
		if tr := incomingTrace(ctx, workflow.Item{JSON: traced("chn_other", 1)}); tr.ChainID != "chn_start" || tr.Hop != 4 {
			t.Fatalf("%s: other-chain item trace = %+v, want the trigger's chain", tt, tr)
		}
	}
	// No trace in the trigger data: the item's is used.
	noTD := workflow.WithTrigger(context.Background(), workflow.TriggerTypeOrgTool, map[string]interface{}{})
	if tr := incomingTrace(noTD, workflow.Item{JSON: traced("chn_item", 2)}); tr.ChainID != "chn_item" || tr.Hop != 2 {
		t.Fatalf("item trace without trigger trace = %+v", tr)
	}
	org := workflow.WithTrigger(context.Background(), workflow.TriggerTypeOrgTool, nil)
	if tr := incomingTrace(org, workflow.Item{JSON: map[string]interface{}{}}); tr.ChainID != "" {
		t.Fatalf("no trace anywhere: %+v, want a fresh chain", tr)
	}
}

// A run whose data mono-agent did not write cannot pick its chain: a manual
// run's input or a schedule gets a fresh chain whatever `trace` it carries,
// even one that also claims an org trigger_type in the data.
func TestIncomingTraceIgnoresTracesFromOutside(t *testing.T) {
	body := traced("chn_victim", float64(0))
	body["trigger_type"] = workflow.TriggerTypeOrgTool // forged in the payload
	for _, tt := range []string{"trigger.manual", "trigger.schedule", workflow.TriggerTypeOrgEvent, ""} {
		ctx := workflow.WithTrigger(context.Background(), tt, body)
		if tr := incomingTrace(ctx, workflow.Item{JSON: body}); tr.ChainID != "" {
			t.Errorf("%q run: joined chain %+v, want a fresh chain", tt, tr)
		}
	}
	if tr := incomingTrace(context.Background(), workflow.Item{JSON: body}); tr.ChainID != "" {
		t.Errorf("no trigger in context: joined chain %+v, want a fresh chain", tr)
	}
}

// A webhook run continues only the chain the webhook server put under the
// reserved key from a verified signed header; a `trace` in the payload
// (the body is the trigger data and the first item) is someone's data.
func TestIncomingTraceForWebhookRuns(t *testing.T) {
	verified := map[string]interface{}{
		workflow.WebhookTraceKey: map[string]interface{}{"chain_id": "chn_loop", "hop": float64(3)},
		"trace":                  map[string]interface{}{"chain_id": "chn_victim", "hop": float64(0)},
	}
	ctx := workflow.WithTrigger(context.Background(), workflow.TriggerNodeTypeWebhook, verified)
	if tr := incomingTrace(ctx, workflow.Item{JSON: verified}); tr.ChainID != "chn_loop" || tr.Hop != 3 {
		t.Fatalf("verified webhook trace = %+v, want chn_loop at 3", tr)
	}
	payloadOnly := traced("chn_victim", float64(0))
	ctx = workflow.WithTrigger(context.Background(), workflow.TriggerNodeTypeWebhook, payloadOnly)
	if tr := incomingTrace(ctx, workflow.Item{JSON: payloadOnly}); tr.ChainID != "" {
		t.Fatalf("payload trace continued as a chain: %+v", tr)
	}
}
