package orgdecide

import (
	"context"
	"testing"

	"github.com/monoes/mono-agent/internal/monomind"
)

// A runtime streams its reply as assistant deltas and the result event has
// no text; monomind.Exec's ResultText then holds only the last delta. The
// decider must parse the whole reply (found in a live run).
func TestModelDeciderParsesStreamedDeltas(t *testing.T) {
	chunks := []string{"```json\n{\"verdict\": \"", "approve", "\", \"rationale", "\": \"policy allows it", "\"}\n```"}
	d := &ModelDecider{Runtime: "claude", Model: "m", Exec: func(ctx context.Context, opts monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		res := &monomind.TurnResult{}
		for _, c := range chunks {
			ev := monomind.Event{Type: monomind.EventAssistant, Text: c}
			onEvent(ev)
			monomind.ApplyEventToResult(res, ev)
		}
		res.CostUSD = 0.01
		return res, nil
	}}
	out, err := d.Decide(context.Background(), Prompt{Allowed: []string{"approve", "deny"}})
	if err != nil {
		t.Fatal(err)
	}
	if out.Verdict.Verdict != "approve" || out.Verdict.Rationale != "policy allows it" || out.CostUSD != 0.01 {
		t.Fatalf("outcome = %+v", out)
	}
}
