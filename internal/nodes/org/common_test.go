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
		if tr := incomingTrace(ctx, workflow.Item{JSON: traced("chn_item", 7)}); tr.ChainID != "chn_item" || tr.Hop != 7 {
			t.Fatalf("%s: item trace = %+v, want the item's own", tt, tr)
		}
	}
	org := workflow.WithTrigger(context.Background(), workflow.TriggerTypeOrgTool, nil)
	if tr := incomingTrace(org, workflow.Item{JSON: map[string]interface{}{}}); tr.ChainID != "" {
		t.Fatalf("no trace anywhere: %+v, want a fresh chain", tr)
	}
}

// A run mono-agent's org side did not start cannot pick its chain: a
// webhook body (which is its trigger data and first item), a manual run's
// input or a schedule gets a fresh chain whatever `trace` it carries, even
// one that also claims an org trigger_type in the data.
func TestIncomingTraceIgnoresTracesFromOutside(t *testing.T) {
	body := traced("chn_victim", float64(0))
	body["trigger_type"] = workflow.TriggerTypeOrgTool // forged in the payload
	for _, tt := range []string{"trigger.webhook", "trigger.manual", "trigger.schedule", ""} {
		ctx := workflow.WithTrigger(context.Background(), tt, body)
		if tr := incomingTrace(ctx, workflow.Item{JSON: body}); tr.ChainID != "" {
			t.Errorf("%q run: joined chain %+v, want a fresh chain", tt, tr)
		}
	}
	if tr := incomingTrace(context.Background(), workflow.Item{JSON: body}); tr.ChainID != "" {
		t.Errorf("no trigger in context: joined chain %+v, want a fresh chain", tr)
	}
}
