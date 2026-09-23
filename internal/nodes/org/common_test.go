package org

import (
	"context"
	"testing"

	"github.com/monoes/mono-agent/internal/workflow"
)

// A node between the trigger and org.send may drop the `trace` field; the
// chain then continues from the execution's trigger data rather than
// starting over, which would reset the hop count of a loop.
func TestIncomingTraceFallsBackToTriggerData(t *testing.T) {
	td := map[string]interface{}{"trace": map[string]interface{}{"chain_id": "chn_start", "hop": float64(4)}}
	ctx := workflow.WithTriggerData(context.Background(), td)

	if tr := incomingTrace(ctx, workflow.Item{JSON: map[string]interface{}{"other": 1}}); tr.ChainID != "chn_start" || tr.Hop != 4 {
		t.Fatalf("fallback trace = %+v", tr)
	}
	own := workflow.Item{JSON: map[string]interface{}{"trace": map[string]interface{}{"chain_id": "chn_item", "hop": 7}}}
	if tr := incomingTrace(ctx, own); tr.ChainID != "chn_item" || tr.Hop != 7 {
		t.Fatalf("item trace = %+v, want the item's own", tr)
	}
	if tr := incomingTrace(context.Background(), workflow.Item{JSON: map[string]interface{}{}}); tr.ChainID != "" {
		t.Fatalf("no trace anywhere: %+v, want a fresh chain", tr)
	}
}
