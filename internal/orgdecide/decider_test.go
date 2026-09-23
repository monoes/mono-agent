package orgdecide

import (
	"context"
	"os"
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

// The decider runs in an empty folder of its own, not the daemon's working
// directory, and the folder is gone afterwards (C-46 live gate).
func TestModelDeciderRunsInAnEmptyFolder(t *testing.T) {
	var cwd string
	d := &ModelDecider{Runtime: "claude", Model: "m", Exec: func(ctx context.Context, opts monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		cwd = opts.Cwd
		entries, err := os.ReadDir(cwd)
		if err != nil || len(entries) != 0 {
			t.Errorf("decider folder %q: %d entries, err %v", cwd, len(entries), err)
		}
		onEvent(monomind.Event{Type: monomind.EventAssistant, Text: `{"verdict": "deny", "rationale": "no"}`})
		return &monomind.TurnResult{}, nil
	}}
	if _, err := d.Decide(context.Background(), Prompt{Allowed: []string{"approve", "deny"}}); err != nil {
		t.Fatal(err)
	}
	wd, _ := os.Getwd()
	if cwd == "" || cwd == wd {
		t.Fatalf("decider ran in %q, want an empty folder of its own", cwd)
	}
	if _, err := os.Stat(cwd); !os.IsNotExist(err) {
		t.Fatalf("decider folder %q left behind", cwd)
	}
}
