package chatevents

import (
	"encoding/json"
	"testing"
	"time"
)

func TestNew_BuildsEnvelopeWithMarshaledPayload(t *testing.T) {
	at := time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC)
	ev, err := New("p1", "conv1", "turn1", 7, at, EventToolCompleted, ToolCompletedPayload{
		CallID: "tc_1",
		OK:     boolPtr(true),
		Result: "ok",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if ev.Version != Version {
		t.Errorf("Version = %d, want %d", ev.Version, Version)
	}
	if ev.ProfileID != "p1" || ev.ConversationID != "conv1" || ev.TurnID != "turn1" {
		t.Errorf("identity fields = %+v, want p1/conv1/turn1", ev)
	}
	if ev.Seq != 7 {
		t.Errorf("Seq = %d, want 7", ev.Seq)
	}
	if ev.At != "2026-09-11T09:00:00.000Z" {
		t.Errorf("At = %q, want the millisecond-precision UTC form", ev.At)
	}
	if ev.Type != EventToolCompleted {
		t.Errorf("Type = %q, want %q", ev.Type, EventToolCompleted)
	}
	var got ToolCompletedPayload
	if err := json.Unmarshal(ev.Payload, &got); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if got.CallID != "tc_1" || got.OK == nil || !*got.OK || got.Result != "ok" {
		t.Errorf("payload round-trip = %+v", got)
	}
}

// Regression guard for the plan's explicit requirement: "False, zero, null
// and empty text are valid outputs" — a nil OK/metric must serialize to
// JSON null and deserialize back to nil, never coerce to false/0.
func TestToolCompletedPayload_NilOKRoundTripsToNullNotFalse(t *testing.T) {
	b, err := json.Marshal(ToolCompletedPayload{CallID: "tc_1", OK: nil, Result: ""})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}
	if v, ok := raw["ok"]; !ok || v != nil {
		t.Errorf(`"ok" = %#v, want explicit JSON null`, v)
	}
	if v, ok := raw["result"]; !ok || v != "" {
		t.Errorf(`"result" = %#v, want present empty string, not absent`, v)
	}

	var got ToolCompletedPayload
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.OK != nil {
		t.Errorf("OK = %v, want nil (absent), not false", *got.OK)
	}
}

// Regression guard for usage: a present-but-zero token count must survive
// as 0, not be indistinguishable from "not reported" (nil).
func TestUsageUpdatedPayload_DistinguishesZeroFromAbsent(t *testing.T) {
	zero := int64(0)
	reported, err := json.Marshal(UsageUpdatedPayload{InputTokens: &zero, Source: "assistant"})
	if err != nil {
		t.Fatalf("marshal reported: %v", err)
	}
	var gotReported UsageUpdatedPayload
	if err := json.Unmarshal(reported, &gotReported); err != nil {
		t.Fatalf("unmarshal reported: %v", err)
	}
	if gotReported.InputTokens == nil || *gotReported.InputTokens != 0 {
		t.Errorf("InputTokens = %v, want a present *0", gotReported.InputTokens)
	}
	if gotReported.CostUSD != nil {
		t.Errorf("CostUSD = %v, want nil (never reported)", *gotReported.CostUSD)
	}

	absent, err := json.Marshal(UsageUpdatedPayload{Source: "assistant"})
	if err != nil {
		t.Fatalf("marshal absent: %v", err)
	}
	var gotAbsent UsageUpdatedPayload
	if err := json.Unmarshal(absent, &gotAbsent); err != nil {
		t.Fatalf("unmarshal absent: %v", err)
	}
	if gotAbsent.InputTokens != nil {
		t.Errorf("InputTokens = %v, want nil when never set", gotAbsent.InputTokens)
	}
}

func TestNew_InvalidPayloadReturnsError(t *testing.T) {
	_, err := New("p1", "c1", "t1", 1, time.Now(), EventNotice, make(chan int))
	if err == nil {
		t.Fatal("New with an unmarshalable payload unexpectedly succeeded")
	}
}

func boolPtr(b bool) *bool { return &b }
