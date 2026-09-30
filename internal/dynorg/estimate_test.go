package dynorg

import (
	"context"
	"math"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/ai/chatevents"
	"github.com/monoes/mono-agent/internal/monomind"
)

var (
	codex5  = Model{Runtime: "codex", Model: "gpt-5", FullAccess: true, Read: true}
	mystery = Model{Runtime: "opencode", Model: "mystery-9", FullAccess: true, Read: true}
)

// noCostTurn is a runtime that reports tokens and, like monomind for
// runtimes that don't report cost (M1 bug 3), a cost of $0.
func noCostTurn(in, out int64) *monomind.TurnResult {
	return &monomind.TurnResult{SawDone: true, ResultText: "done", StopReason: monomind.StopEndTurn,
		InputTokens: in, OutputTokens: out, HasInputTokens: true, HasOutputTokens: true, HasCostUSD: true}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestRunCost(t *testing.T) {
	cases := []struct {
		name      string
		m         Model
		run       monomind.TurnResult
		cost      float64
		estimated bool
		ok        bool
	}{
		{"reported cost wins", codex5, monomind.TurnResult{CostUSD: 0.02, HasCostUSD: true, InputTokens: 100_000, HasInputTokens: true}, 0.02, false, true},
		{"$0 next to tokens is estimated", codex5, *noCostTurn(100_000, 10_000), 0.35, true, true},
		{"no cost key, tokens", codex5, monomind.TurnResult{InputTokens: 100_000, OutputTokens: 10_000, HasInputTokens: true, HasOutputTokens: true}, 0.35, true, true},
		{"$0 from a runtime that reports cost is real", Model{Runtime: "codex", Model: "gpt-5", ReportsCost: true}, *noCostTurn(100_000, 10_000), 0, false, true},
		{"$0 and no tokens stays $0", codex5, monomind.TurnResult{HasCostUSD: true}, 0, false, true},
		{"unknown model: no estimate", mystery, monomind.TurnResult{InputTokens: 100_000, HasInputTokens: true}, 0, false, false},
		{"unknown model keeps its $0", mystery, *noCostTurn(100_000, 10_000), 0, false, true},
		{"neither cost nor tokens", codex5, monomind.TurnResult{}, 0, false, false},
	}
	for _, c := range cases {
		cost, est, ok := runCost(c.m, &c.run)
		if !near(cost, c.cost) || est != c.estimated || ok != c.ok {
			t.Errorf("%s: runCost = %v, %v, %v; want %v, %v, %v", c.name, cost, est, ok, c.cost, c.estimated, c.ok)
		}
	}
}

func TestEstimatedCostCountsTowardBudget(t *testing.T) {
	ex := &execScript{answers: map[string]*monomind.TurnResult{"codex/gpt-5": noCostTurn(100_000, 10_000)}}
	c, em := newTestConductor(t, ex, Limits{MaxAgents: 5, MaxConcurrent: 1, BudgetUSD: 0.5}, codex5)
	if _, err := c.Spawn(context.Background(), SpawnRequest{Brief: "implement a", Wait: true}); err != nil {
		t.Fatal(err)
	}
	fin := em.find(chatevents.EventAgentFinished)[0].(chatevents.AgentFinishedPayload)
	if fin.CostUSD == nil || !near(*fin.CostUSD, 0.35) || !fin.CostEstimated {
		t.Fatalf("finished = %+v, want ≈$0.35 estimated", fin)
	}
	// The next exec gets what the estimate left of the budget.
	if _, err := c.Spawn(context.Background(), SpawnRequest{Brief: "implement b", Wait: true}); err != nil {
		t.Fatal(err)
	}
	if got := ex.calls[1].BudgetUSD; !near(got, 0.15) {
		t.Errorf("second exec budget = %v, want 0.15", got)
	}
	// ≈$0.70 of $0.50: the budget stop triggers.
	_, err := c.Spawn(context.Background(), SpawnRequest{Brief: "implement c"})
	if err == nil || !strings.Contains(err.Error(), "budget") || !strings.Contains(err.Error(), "estimated") {
		t.Errorf("third spawn err = %v, want the budget stop, saying it is estimated", err)
	}
	if v := c.Roster(); v.Limits["spent_estimated"] != true {
		t.Errorf("roster limits = %v, want spent_estimated", v.Limits)
	}
}

func TestRealCostWinsOverEstimate(t *testing.T) {
	real := noCostTurn(100_000, 10_000)
	real.CostUSD = 0.01
	ex := &execScript{answers: map[string]*monomind.TurnResult{"codex/gpt-5": real}}
	c, em := newTestConductor(t, ex, Limits{MaxAgents: 5, MaxConcurrent: 1, BudgetUSD: 0.05}, codex5)
	if _, err := c.Spawn(context.Background(), SpawnRequest{Brief: "implement a", Wait: true}); err != nil {
		t.Fatal(err)
	}
	fin := em.find(chatevents.EventAgentFinished)[0].(chatevents.AgentFinishedPayload)
	if fin.CostUSD == nil || *fin.CostUSD != 0.01 || fin.CostEstimated {
		t.Errorf("finished = %+v, want the reported $0.01", fin)
	}
	if _, err := c.Spawn(context.Background(), SpawnRequest{Brief: "implement b"}); err != nil {
		t.Errorf("$0.01 of $0.05 spent, spawn err = %v", err)
	}
}

func TestUnknownModelIsNotEstimated(t *testing.T) {
	ex := &execScript{answers: map[string]*monomind.TurnResult{"opencode/mystery-9": {SawDone: true, ResultText: "done",
		InputTokens: 10_000_000, OutputTokens: 1_000_000, HasInputTokens: true, HasOutputTokens: true}}}
	c, em := newTestConductor(t, ex, Limits{MaxAgents: 5, MaxConcurrent: 1, BudgetUSD: 0.01}, mystery)
	if _, err := c.Spawn(context.Background(), SpawnRequest{Brief: "implement a", Wait: true}); err != nil {
		t.Fatal(err)
	}
	fin := em.find(chatevents.EventAgentFinished)[0].(chatevents.AgentFinishedPayload)
	if fin.CostUSD != nil || fin.CostEstimated {
		t.Errorf("finished = %+v, want no cost", fin)
	}
	// Nothing counted: as before, the budget can't stop it.
	if _, err := c.Spawn(context.Background(), SpawnRequest{Brief: "implement b"}); err != nil {
		t.Errorf("spawn err = %v", err)
	}
}

// A running worker whose estimate reaches its budget is stopped: monomind
// can't enforce --budget-usd on a cost it doesn't see.
func TestEstimateStopsRunningWorker(t *testing.T) {
	em := &recEmitter{}
	exec := func(ctx context.Context, o monomind.ExecOptions, on func(monomind.Event)) (*monomind.TurnResult, error) {
		on(monomind.Event{Type: monomind.EventStart})
		on(monomind.Event{Type: monomind.EventAssistant, Text: "half done"})
		on(monomind.Event{Type: monomind.EventUsage, InputTokens: 100_000, OutputTokens: 10_000, HasInputTokens: true, HasOutputTokens: true, HasCostUSD: true})
		<-ctx.Done() // runs until it is stopped; cancelled, it reports no usage
		return &monomind.TurnResult{SawDone: true, ResultText: "half done", StopReason: monomind.StopCancelled}, nil
	}
	c := New(context.Background(), Config{Cwd: "/w", Staffer: &Staffer{Roster: []Model{codex5}, Lead: codex5},
		Exec: exec, Emit: em, Limits: Limits{MaxAgents: 3, MaxConcurrent: 1, BudgetUSD: 0.3}})
	t.Cleanup(c.Close)
	info, err := c.Spawn(context.Background(), SpawnRequest{Brief: "implement it", Wait: true})
	if err != nil {
		t.Fatal(err)
	}
	if info.Status != chatevents.AgentFailed || !strings.HasPrefix(info.Error, "budget") {
		t.Errorf("info = %+v, want failed by the budget", info)
	}
	fin := em.find(chatevents.EventAgentFinished)[0].(chatevents.AgentFinishedPayload)
	if fin.CostUSD == nil || !near(*fin.CostUSD, 0.35) || !fin.CostEstimated {
		t.Errorf("finished = %+v, want ≈$0.35 counted from the usage events", fin)
	}
	var live *chatevents.UsageUpdatedPayload
	for _, p := range em.find(chatevents.EventUsageUpdated) {
		u := p.(chatevents.UsageUpdatedPayload)
		live = &u
	}
	if live == nil || live.CostUSD == nil || !near(*live.CostUSD, 0.35) || !live.CostEstimated {
		t.Errorf("usage.updated = %+v, want ≈$0.35 estimated", live)
	}
	if _, err := c.Spawn(context.Background(), SpawnRequest{Brief: "more"}); err == nil || !strings.Contains(err.Error(), "budget") {
		t.Errorf("spawn after the stop err = %v", err)
	}
}

// An estimate that reaches the budget only on the run's last usage event
// doesn't turn a completed run into a failure: it stays done, its cost
// counts, and the budget refuses what comes next.
func TestEstimateAtTheEndKeepsACompletedRunDone(t *testing.T) {
	em := &recEmitter{}
	exec := func(ctx context.Context, o monomind.ExecOptions, on func(monomind.Event)) (*monomind.TurnResult, error) {
		on(monomind.Event{Type: monomind.EventStart})
		on(monomind.Event{Type: monomind.EventResult, Text: "all done", StopReason: monomind.StopEndTurn,
			InputTokens: 100_000, OutputTokens: 10_000, HasInputTokens: true, HasOutputTokens: true, HasCostUSD: true})
		r := noCostTurn(100_000, 10_000)
		r.ResultText = "all done"
		return r, nil
	}
	c := New(context.Background(), Config{Cwd: "/w", Staffer: &Staffer{Roster: []Model{codex5}, Lead: codex5},
		Exec: exec, Emit: em, Limits: Limits{MaxAgents: 3, MaxConcurrent: 1, BudgetUSD: 0.3}})
	t.Cleanup(c.Close)
	info, err := c.Spawn(context.Background(), SpawnRequest{Brief: "implement it", Wait: true})
	if err != nil {
		t.Fatal(err)
	}
	if info.Status != chatevents.AgentDone || info.Report != "all done" {
		t.Errorf("info = %+v, want done", info)
	}
	fin := em.find(chatevents.EventAgentFinished)[0].(chatevents.AgentFinishedPayload)
	if fin.CostUSD == nil || !near(*fin.CostUSD, 0.35) || !fin.CostEstimated {
		t.Errorf("finished = %+v, want ≈$0.35 counted", fin)
	}
	_, err = c.Spawn(context.Background(), SpawnRequest{Brief: "more"})
	if err == nil || !strings.Contains(err.Error(), "partly estimated") {
		t.Errorf("next spawn err = %v, want the budget refusal", err)
	}
}

// With no budget set (the default), an estimate is shown but never
// refuses or stops a worker: it may be a subscription runtime.
func TestEstimateWithoutBudgetNeverStops(t *testing.T) {
	ex := &execScript{answers: map[string]*monomind.TurnResult{"codex/gpt-5": noCostTurn(10_000_000, 1_000_000)}}
	c, em := newTestConductor(t, ex, Limits{MaxAgents: 3, MaxConcurrent: 1}, codex5)
	for _, brief := range []string{"implement a", "implement b", "implement c"} {
		info, err := c.Spawn(context.Background(), SpawnRequest{Brief: brief, Wait: true})
		if err != nil || info.Status != chatevents.AgentDone {
			t.Fatalf("%s: %+v, %v", brief, info, err)
		}
	}
	if ex.calls[2].BudgetUSD != 0 {
		t.Errorf("exec budget = %v, want none", ex.calls[2].BudgetUSD)
	}
	for _, p := range em.find(chatevents.EventAgentFinished) {
		if f := p.(chatevents.AgentFinishedPayload); f.CostUSD == nil || !near(*f.CostUSD, 35) || !f.CostEstimated {
			t.Errorf("finished = %+v, want ≈$35 shown", f)
		}
	}
}
