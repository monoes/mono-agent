package monomind

import (
	"encoding/json"
	"testing"
)

// TestEventUnmarshalJSON_MetricPresence is a direct unit test of the
// decode-side presence tracking, independent of the subprocess-driven
// exec_test.go coverage: a key present with value 0 must set Has*=true,
// and an absent key must leave Has*=false, for all three optional metrics
// independently of one another.
func TestEventUnmarshalJSON_MetricPresence(t *testing.T) {
	var ev Event
	line := `{"v":1,"type":"result","cost_usd":0,"input_tokens":10}`
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !ev.HasCostUSD || ev.CostUSD != 0 {
		t.Errorf("HasCostUSD=%v CostUSD=%v, want true/0", ev.HasCostUSD, ev.CostUSD)
	}
	if !ev.HasInputTokens || ev.InputTokens != 10 {
		t.Errorf("HasInputTokens=%v InputTokens=%v, want true/10", ev.HasInputTokens, ev.InputTokens)
	}
	if ev.HasOutputTokens {
		t.Errorf("HasOutputTokens = true, want false — output_tokens was never in the JSON")
	}
}

// TestEventMarshalJSON_OmitsAbsentMetrics verifies the encode side stays
// symmetric with decode: a metric that was never present must not
// reappear as a fabricated 0 on re-encode.
func TestEventMarshalJSON_OmitsAbsentMetrics(t *testing.T) {
	ev := Event{V: 1, Type: "result", HasCostUSD: true, CostUSD: 0.5}
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}
	if v, ok := raw["cost_usd"]; !ok || v != 0.5 {
		t.Errorf(`"cost_usd" = %#v, want present 0.5`, v)
	}
	if _, ok := raw["input_tokens"]; ok {
		t.Errorf(`"input_tokens" present in output, want absent (never reported)`)
	}
	if _, ok := raw["output_tokens"]; ok {
		t.Errorf(`"output_tokens" present in output, want absent (never reported)`)
	}
}

// TestEventJSON_RoundTripPreservesPresence chains decode then encode: a
// line missing a metric must come back out still missing it, not
// zero-filled.
func TestEventJSON_RoundTripPreservesPresence(t *testing.T) {
	original := `{"v":1,"type":"usage","input_tokens":100,"output_tokens":20}`
	var ev Event
	if err := json.Unmarshal([]byte(original), &ev); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}
	if _, ok := raw["cost_usd"]; ok {
		t.Errorf(`"cost_usd" reappeared after round-trip, want it to stay absent`)
	}
}
