package dynorg

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/monoes/mono-agent/internal/ai/chatevents"
	"github.com/monoes/mono-agent/internal/monomind"
)

// chattyExec is a fake agent exec that talks: text, a tool call, more
// text, and usage snapshots before its result.
func chattyExec(texts ...string) ExecFunc {
	return func(ctx context.Context, o monomind.ExecOptions, on func(monomind.Event)) (*monomind.TurnResult, error) {
		on(monomind.Event{Type: monomind.EventStart})
		on(monomind.Event{Type: monomind.EventAssistant, Text: "Looking "})
		on(monomind.Event{Type: monomind.EventAssistant, Text: "at the cache."})
		on(monomind.Event{Type: monomind.EventToolActivity, CoderFields: monomind.CoderFields{Phase: "start", Input: json.RawMessage(`{"file_path":"a.go"}`)}, ID: "t1", Name: "Read"})
		on(monomind.Event{Type: monomind.EventToolActivity, CoderFields: monomind.CoderFields{Phase: "end"}, ID: "t1", Name: "Read"})
		on(monomind.Event{Type: monomind.EventUsage, InputTokens: 100, HasInputTokens: true, OutputTokens: 10, HasOutputTokens: true})
		for _, t := range texts {
			on(monomind.Event{Type: monomind.EventAssistant, Text: t})
		}
		on(monomind.Event{Type: monomind.EventResult, InputTokens: 150, HasInputTokens: true, OutputTokens: 30, HasOutputTokens: true, CostUSD: 0.02, HasCostUSD: true})
		r := okTurn("It is in a.go.")
		r.InputTokens, r.HasInputTokens, r.OutputTokens, r.HasOutputTokens, r.CostUSD = 150, true, 30, true, 0.02
		return r, nil
	}
}

func newStreamConductor(t *testing.T, ex ExecFunc, roster ...Model) (*Conductor, *recEmitter) {
	t.Helper()
	em := &recEmitter{}
	if len(roster) == 0 {
		roster = []Model{opus, haiku}
	}
	c := New(context.Background(), Config{Cwd: "/w", Staffer: &Staffer{Roster: roster, Lead: roster[0]}, ReadAccess: true, Exec: ex, Emit: em})
	t.Cleanup(c.Close)
	return c, em
}

func TestWorkerTextAndUsageAreJournaledWithItsAgentID(t *testing.T) {
	c, em := newStreamConductor(t, chattyExec("It is ", "in a.go."))
	if _, err := c.Spawn(context.Background(), SpawnRequest{Brief: "find the cache", Wait: true}); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(em.types("w1"), " ")
	want := "agent.spawned agent.message agent.status:queued agent.status:starting agent.status:working " +
		"assistant.delta tool.started tool.completed usage.updated assistant.delta usage.updated " +
		"agent.status:done agent.message agent.finished"
	if got != want {
		t.Errorf("events\n got %s\nwant %s", got, want)
	}
	var parts []string
	for _, p := range em.find(chatevents.EventAssistantDelta) {
		d := p.(chatevents.AssistantDeltaPayload)
		parts = append(parts, d.AgentID+"|"+d.PartID+"|"+d.Text)
	}
	if strings.Join(parts, ",") != "w1|w1:p1|Looking at the cache.,w1|w1:p2|It is in a.go." {
		t.Errorf("text parts = %v", parts)
	}
	// Usage is the worker's running total, tagged, never the lead's.
	var usage []string
	for _, p := range em.find(chatevents.EventUsageUpdated) {
		u := p.(chatevents.UsageUpdatedPayload)
		if u.AgentID != "w1" || u.InputTokens == nil {
			t.Fatalf("usage = %+v", u)
		}
		s := u.AgentID + ":" + itoa(*u.InputTokens) + "/" + itoa(*u.OutputTokens)
		if u.CostUSD != nil {
			s += "$"
		}
		usage = append(usage, s)
	}
	if strings.Join(usage, ",") != "w1:100/10,w1:150/30$" {
		t.Errorf("usage = %v", usage)
	}
}

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func TestWorkerUsageCountsEarlierExecs(t *testing.T) {
	calls := 0
	ex := func(ctx context.Context, o monomind.ExecOptions, on func(monomind.Event)) (*monomind.TurnResult, error) {
		calls++
		on(monomind.Event{Type: monomind.EventStart})
		on(monomind.Event{Type: monomind.EventUsage, InputTokens: 10, HasInputTokens: true, CostUSD: 0.01, HasCostUSD: true})
		r := okTurn("done")
		r.InputTokens, r.HasInputTokens = 10, true
		return r, nil
	}
	c, em := newStreamConductor(t, ex)
	if _, err := c.Spawn(context.Background(), SpawnRequest{Brief: "implement it", Wait: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Message(context.Background(), "w1", "and the tests"); err != nil {
		t.Fatal(err)
	}
	c.Wait(context.Background(), []string{"w1"}, MaxWait)
	us := em.find(chatevents.EventUsageUpdated)
	last := us[len(us)-1].(chatevents.UsageUpdatedPayload)
	if calls != 2 || *last.InputTokens != 20 || *last.CostUSD != 0.02 {
		t.Errorf("calls %d, last usage = in %d cost %v", calls, *last.InputTokens, *last.CostUSD)
	}
}

func TestWorkerTextIsBounded(t *testing.T) {
	big := strings.Repeat("é", MaxWorkerText) // 2 bytes each
	c, em := newStreamConductor(t, chattyExec(big, "never journaled"))
	if _, err := c.Spawn(context.Background(), SpawnRequest{Brief: "write a lot", Wait: true}); err != nil {
		t.Fatal(err)
	}
	var total strings.Builder
	for _, p := range em.find(chatevents.EventAssistantDelta) {
		total.WriteString(p.(chatevents.AssistantDeltaPayload).Text)
	}
	s := total.String()
	if !utf8.ValidString(s) || !strings.HasSuffix(s, textCutNote) || strings.Contains(s, "never journaled") {
		t.Errorf("bounded text: valid=%v len=%d suffix=%q", utf8.ValidString(s), len(s), s[max(0, len(s)-60):])
	}
	if len(s) > MaxWorkerText+len(textCutNote)+len("Looking at the cache.") {
		t.Errorf("journaled %d bytes, over the bound", len(s))
	}
}

func TestSpawnedAndReassignedCarryFidelity(t *testing.T) {
	full := opus
	full.Fidelity = "full"
	startsOnly := haiku
	startsOnly.Fidelity = "start-only"
	ex := &execScript{answers: map[string]*monomind.TurnResult{
		"claude/opus": {SawDone: true, Err: &monomind.ProtocolError{Code: monomind.ErrAuth, Message: "Not logged in"}},
	}}
	c, em := newStreamConductor(t, ex.exec, full, startsOnly)
	if _, err := c.Spawn(context.Background(), SpawnRequest{Brief: "implement", Runtime: "claude", Model: "opus", Wait: true}); err != nil {
		t.Fatal(err)
	}
	sp := em.find(chatevents.EventAgentSpawned)[0].(chatevents.AgentSpawnedPayload)
	re := em.find(chatevents.EventAgentReassigned)[0].(chatevents.AgentReassignedPayload)
	if sp.Fidelity != "full" || re.Fidelity != "start-only" || re.ToModel != "haiku" {
		t.Errorf("spawned fidelity %q, reassigned %+v", sp.Fidelity, re)
	}
}
