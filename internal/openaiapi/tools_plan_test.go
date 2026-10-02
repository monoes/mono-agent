package openaiapi

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/monomind"
)

// planFixture is a follow-up request that answers one call of an earlier leg,
// the model that ran the leg, a principal, and the record the leg left.
func planFixture(t *testing.T, messages string) (*harness, *ChatRequest, ModelInfo, Principal, contRecord) {
	t.Helper()
	h := newHarness(t, okTurn("x"))
	req := toolRequest(t, `"tools":[`+weatherTool+`]`, messages)
	m := ModelInfo{ID: "claude/default", Runtime: "claude", Model: "default", Class: ChatOnly}
	pr := Principal{KeyID: "key_a", ProfileID: "alice"}
	rec := contRecord{CallID: "call_a", KeyID: "key_a", ProfileID: "alice", Model: "claude/default", Name: "get_weather",
		Session: "sess-1", ToolsHash: toolsHash(req.toolDecls)}
	return h, req, m, pr, rec
}

const oneRound = `{"role":"user","content":"Weather in Paris?"},{"role":"assistant","tool_calls":[` + callParis + `]},{"role":"tool","tool_call_id":"call_a","content":"21"}`

func TestPlanLegOfAFirstRequestIsThePlainPrompt(t *testing.T) {
	h, req, m, pr, _ := planFixture(t, `{"role":"user","content":"Weather in Paris?"}`)
	got := h.g.planLeg(pr, req, m, "the plain prompt")
	if got.Kind != legFirst || got.Prompt != "the plain prompt" || got.Session != "" {
		t.Errorf("plan: %+v", got)
	}
}

func TestPlanLegResumesTheSessionOfAMatchingRecord(t *testing.T) {
	h, req, m, pr, rec := planFixture(t, oneRound)
	h.g.conts.put(rec)
	got := h.g.planLeg(pr, req, m, "unused")
	if got.Kind != legResume || got.Session != "sess-1" || got.Prompt != resumePrompt(req, 1) {
		t.Fatalf("plan: %+v", got)
	}
	// The record is used up: the same follow-up again starts from its transcript.
	again := h.g.planLeg(pr, req, m, "unused")
	if again.Kind != legReplay || again.Session != "" || again.Prompt != replayPrompt(req, true) {
		t.Errorf("a retry must replay: %+v", again)
	}
}

func TestPlanLegReplaysWhenNothingMatches(t *testing.T) {
	cases := []struct {
		name     string
		messages string
		mutate   func(r *contRecord, m *ModelInfo, pr *Principal)
		wait     time.Duration
	}{
		{name: "there is no record", mutate: func(r *contRecord, _ *ModelInfo, _ *Principal) { r.CallID = "call_other" }},
		{name: "another key", mutate: func(_ *contRecord, _ *ModelInfo, pr *Principal) { pr.KeyID = "key_b" }},
		{name: "another profile", mutate: func(_ *contRecord, _ *ModelInfo, pr *Principal) { pr.ProfileID = "bob" }},
		{name: "another model", mutate: func(_ *contRecord, m *ModelInfo, _ *Principal) { m.ID = "claude/opus" }},
		{name: "another function", mutate: func(r *contRecord, _ *ModelInfo, _ *Principal) { r.Name = "get_stock" }},
		{name: "other tools", mutate: func(r *contRecord, _ *ModelInfo, _ *Principal) { r.ToolsHash = "another" }},
		{name: "an expired record", wait: contTTL + time.Second},
		{name: "a call that was not answered", messages: `{"role":"user","content":"q"},{"role":"assistant","tool_calls":[` + callParis + `]},{"role":"user","content":"hello?"}`},
		{name: "two calls in the message", messages: `{"role":"user","content":"q"},{"role":"assistant","tool_calls":[` + callParis + `,` + callRome + `]},{"role":"tool","tool_call_id":"call_a","content":"1"},{"role":"tool","tool_call_id":"call_b","content":"2"}`},
		{name: "two calls in the message, one answered", messages: `{"role":"user","content":"q"},{"role":"assistant","tool_calls":[` + callParis + `,` + callRome + `]},{"role":"tool","tool_call_id":"call_a","content":"1"}`},
		{name: "a result of another call", messages: `{"role":"user","content":"q"},{"role":"assistant","tool_calls":[` + callParis + `]},{"role":"tool","tool_call_id":"call_a","content":"1"},{"role":"assistant","tool_calls":[` + callRome + `]},{"role":"tool","tool_call_id":"call_a","content":"stale"}`,
			mutate: func(r *contRecord, _ *ModelInfo, _ *Principal) { r.CallID = "call_b" }},
		{name: "a conversation that went on after the answer", messages: oneRound + `,{"role":"assistant","content":"It is 21."},{"role":"user","content":"And Rome?"}`},
	}
	for _, c := range cases {
		messages := c.messages
		if messages == "" {
			messages = oneRound
		}
		h, req, m, pr, rec := planFixture(t, messages)
		clock := newTestClock()
		h.g.conts.now = clock.now
		if c.mutate != nil {
			c.mutate(&rec, &m, &pr)
		}
		h.g.conts.put(rec)
		clock.advance(c.wait)
		got := h.g.planLeg(pr, req, m, "unused")
		if got.Kind != legReplay || got.Session != "" || !strings.HasPrefix(got.Prompt, transcriptIntro) {
			t.Errorf("%s: plan %+v, want a replay", c.name, got)
		}
	}
}

// execScript answers the Exec calls of a test one after the other and records the
// options each was given.
type execScript struct {
	mu    sync.Mutex
	opts  []monomind.ExecOptions
	turns []execFunc
}

func (s *execScript) exec(ctx context.Context, o monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
	s.mu.Lock()
	n := len(s.opts)
	s.opts = append(s.opts, o)
	s.mu.Unlock()
	if n >= len(s.turns) {
		return nil, errors.New("more turns than the test scripted")
	}
	return s.turns[n](ctx, o, onEvent)
}

func (s *execScript) calls() []monomind.ExecOptions {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]monomind.ExecOptions(nil), s.opts...)
}

// A session the runtime does not know ends the way claude's and codex's do: a
// runner error, exit 1, nothing said.
func unknownSession() execFunc {
	return scriptedExec(evStart(false, "monomind"), evSession("sess-1"),
		monomind.Event{V: 1, Type: monomind.EventError, Code: monomind.ErrRunnerError, ErrMessage: "No conversation found with session ID: sess-1"}, evDone(1))
}

func callsFirstTool() execFunc {
	return fakeLegExec(func(ctx context.Context, emit func(monomind.Event)) {
		emit(evStart(false, "monomind"))
		emit(evSession("sess-2"))
		emit(evCall("get_weather", `{"city":"Rome"}`))
		<-ctx.Done()
	})
}

func TestRunPlannedFallsBackToAReplayWhenTheSessionIsGone(t *testing.T) {
	shortGrace(t)
	script := &execScript{turns: []execFunc{unknownSession(), okTurn("It is 21 in Paris.")}}
	h := newHarness(t, script.exec)
	req := toolRequest(t, `"tools":[`+weatherTool+`]`, oneRound)
	tn := legTurn("claude")
	plan := legPlan{Kind: legResume, Prompt: resumePrompt(req, 1), Session: "sess-1"}

	got := h.g.runPlanned(context.Background(), tn, plan, req)
	calls := script.calls()
	if len(calls) != 2 {
		t.Fatalf("Exec was called %d times, want the resume and then the replay", len(calls))
	}
	if calls[0].Resume != "sess-1" || calls[0].Prompt != plan.Prompt {
		t.Errorf("the first call: resume %q prompt %q", calls[0].Resume, calls[0].Prompt)
	}
	if calls[1].Resume != "" || calls[1].Prompt != replayPrompt(req, true) {
		t.Errorf("the second call must start from the transcript: resume %q prompt %q", calls[1].Resume, calls[1].Prompt)
	}
	if got.Kind != legReplay || !got.FellBack || got.Res == nil || got.Res.ResultText != "It is 21 in Paris." {
		t.Errorf("result: kind %s fellBack %v %+v", got.Kind, got.FellBack, got.Res)
	}
	if len(calls[1].Tools) != 1 {
		t.Error("the replay declares the tools again")
	}
}

func TestRunPlannedDoesNotRetryWhatIsNotTheSessionsFault(t *testing.T) {
	shortGrace(t)
	quota := scriptedExec(evStart(false, "monomind"), evError(monomind.ErrQuota, "usage limit"), evDone(1))
	spoke := scriptedExec(evStart(false, "monomind"), evText("It is"), evError(monomind.ErrRunnerError, "boom"), evDone(1))
	for name, first := range map[string]execFunc{"quota": quota, "a runner error after the model spoke": spoke, "a call": callsFirstTool()} {
		script := &execScript{turns: []execFunc{first, okTurn("never")}}
		h := newHarness(t, script.exec)
		req := toolRequest(t, `"tools":[`+weatherTool+`]`, oneRound)
		plan := legPlan{Kind: legResume, Prompt: resumePrompt(req, 1), Session: "sess-1"}
		got := h.g.runPlanned(context.Background(), legTurn("claude"), plan, req)
		if n := len(script.calls()); n != 1 || got.FellBack || got.Kind != legResume {
			t.Errorf("%s: %d Exec calls, fellBack %v, kind %s", name, n, got.FellBack, got.Kind)
		}
	}
}

func TestRunPlannedRunsAReplayOrAFirstLegOnce(t *testing.T) {
	for _, kind := range []string{legFirst, legReplay} {
		script := &execScript{turns: []execFunc{unknownSession(), okTurn("never")}}
		h := newHarness(t, script.exec)
		req := toolRequest(t, `"tools":[`+weatherTool+`]`, oneRound)
		got := h.g.runPlanned(context.Background(), legTurn("claude"), legPlan{Kind: kind, Prompt: "p"}, req)
		if n := len(script.calls()); n != 1 || got.FellBack || got.Kind != kind {
			t.Errorf("%s: %d Exec calls, fellBack %v, kind %s", kind, n, got.FellBack, got.Kind)
		}
		if script.calls()[0].Resume != "" {
			t.Errorf("%s must not resume anything", kind)
		}
	}
}

// Asking with the wrong key, model or tools must not use up the record of whoever
// it belongs to.
func TestPlanLegThatDoesNotMatchLeavesTheRecordForItsOwner(t *testing.T) {
	h, req, m, pr, rec := planFixture(t, oneRound)
	h.g.conts.put(rec)
	stranger := pr
	stranger.KeyID = "key_b"
	if got := h.g.planLeg(stranger, req, m, "x"); got.Kind != legReplay {
		t.Fatalf("a stranger resumed: %+v", got)
	}
	if got := h.g.planLeg(pr, req, m, "x"); got.Kind != legResume {
		t.Errorf("the owner lost its record: %+v", got)
	}
}
