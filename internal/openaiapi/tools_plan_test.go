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
	if ai, ok := trailingRound(req); ok { // the session saw the conversation as it was before the call
		rec.Convo = convoHash(req, ai)
	}
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

// A session is continued only when the conversation is the one the session saw. The
// client may have edited or compacted its history, changed the system prompt or the
// choice of tool since; a session resumed on the result alone would go on from a
// conversation that is no longer the client's, and a stricter system prompt would
// be ignored. Whatever the client does not change in substance (an empty content for
// a null one, trailing whitespace, the spacing of the arguments) must not cost a replay.
const (
	sysBrief = `{"role":"system","content":"Be brief."}`
	askParis = `{"role":"user","content":"Weather in Paris?"}`
	askRome  = `{"role":"user","content":"Weather in Rome?"}`
	hello    = `{"role":"user","content":"hello"},{"role":"assistant","content":"hi!"}`
	paris21  = `{"role":"tool","tool_call_id":"call_a","content":"21"}`
	rome25   = `{"role":"tool","tool_call_id":"call_b","content":"25"}`

	callParisMsg    = `{"role":"assistant","content":null,"tool_calls":[` + callParis + `]}`
	callRomeMsg     = `{"role":"assistant","content":null,"tool_calls":[` + callRome + `]}`
	parisNarrated   = `{"role":"assistant","content":"Checking.\n","tool_calls":[` + callParis + `]}`
	parisTrimmed    = `{"role":"assistant","content":"Checking.","tool_calls":[` + callParis + `]}`
	parisEmpty      = `{"role":"assistant","content":"","tool_calls":[` + callParis + `]}`
	parisSpaced     = `{"role":"assistant","content":null,"tool_calls":[{"id":"call_a","type":"function","function":{"name":"get_weather","arguments":"{ \"city\": \"Paris\" }"}}]}`
	askParisAsParts = `{"role":"user","content":[{"type":"text","text":"Weather in Paris?"}]}`

	stockTool      = `{"type":"function","function":{"name":"get_stock","parameters":{"type":"object","properties":{}}}}`
	callOslo       = `{"id":"call_c","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Oslo\"}"}}`
	callOsloMsg    = `{"role":"assistant","content":null,"tool_calls":[` + callOslo + `]}`
	oslo3          = `{"role":"tool","tool_call_id":"call_c","content":"3"}`
	twoCallsMsg    = `{"role":"assistant","content":null,"tool_calls":[` + callParis + `,` + callRome + `]}`
	okA            = `{"role":"tool","tool_call_id":"call_a","content":"ok"}`
	okB            = `{"role":"tool","tool_call_id":"call_b","content":"ok"}`
	parisOtherArgs = `{"role":"assistant","content":null,"tool_calls":[{"id":"call_a","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Rome\"}"}}]}`
	callRomeZ      = `{"id":"call_z","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Rome\"}"}}`
	callRomeStock  = `{"id":"call_b","type":"function","function":{"name":"get_stock","arguments":"{\"city\":\"Rome\"}"}}`
	namedWeather   = `,"tool_choice":{"type":"function","function":{"name":"get_weather"}}`
	namedStock     = `,"tool_choice":{"type":"function","function":{"name":"get_stock"}}`
)

// planAfter plans the follow-up (followMsgs, with followExtra among its parameters)
// of a call (callID) that a leg made when the request said firstMsgs and firstExtra.
func planAfter(t *testing.T, callID, firstExtra, firstMsgs, followExtra, followMsgs string) legPlan {
	t.Helper()
	h, _, m, pr, rec := planFixture(t, oneRound)
	tools := `"tools":[` + weatherTool + `,` + stockTool + `]`
	first := toolRequest(t, tools+firstExtra, firstMsgs)
	follow := toolRequest(t, tools+followExtra, followMsgs)
	rec.CallID, rec.Convo, rec.ToolsHash = callID, convoHash(first, len(first.Messages)), toolsHash(first.toolDecls)
	h.g.conts.put(rec)
	return h.g.planLeg(pr, follow, m, "unused")
}

func TestPlanLegResumesOnlyTheConversationTheSessionSaw(t *testing.T) {
	first := sysBrief + "," + askParis
	followed := first + "," + callParisMsg + "," + paris21
	const required = `,"tool_choice":"required"`
	cases := []struct {
		name                    string
		callID                  string
		firstExtra, firstMsgs   string
		followExtra, followMsgs string
		want                    string
	}{
		{"the conversation as it was", "call_a", "", first, "", followed, legResume},
		{"a user message after the result", "call_a", "", first, "", followed + `,{"role":"user","content":"Also in French."}`, legResume},
		{"the same required choice", "call_a", required, first, required, followed, legResume},

		{"the first message edited", "call_a", "", first, "", sysBrief + "," + askRome + "," + callParisMsg + "," + paris21, legReplay},
		{"the system prompt changed", "call_a", "", first, "", strings.Replace(followed, "Be brief.", "Be verbose.", 1), legReplay},
		{"the system prompt dropped", "call_a", "", first, "", askParis + "," + callParisMsg + "," + paris21, legReplay},
		{"a message compacted away", "call_a", "", sysBrief + "," + hello + "," + askParis, "", followed, legReplay},
		{"a message added before the call", "call_a", "", first, "", sysBrief + "," + hello + "," + askParis + "," + callParisMsg + "," + paris21, legReplay},
		{"the choice became required", "call_a", "", first, required, followed, legReplay},
		{"the choice became a named function", "call_a", "", first, `,"tool_choice":{"type":"function","function":{"name":"get_weather"}}`, followed, legReplay},
		{"the required choice was lifted", "call_a", required, first, "", followed, legReplay},
		{"the named function is the same", "call_a", namedWeather, first, namedWeather, followed, legResume},
		{"the named function changed", "call_a", namedWeather, first, namedStock, followed, legReplay},
		{"a system prompt sent as a user message", "call_a", "", first, "", `{"role":"user","content":"Be brief."},` + askParis + "," + callParisMsg + "," + paris21, legReplay},
		{"the response format changed", "call_a", "", first, `,"response_format":{"type":"json_object"}`, followed, legReplay},
		{"a system message after the result", "call_a", "", first, "", followed + `,{"role":"system","content":"New rule."}`, legReplay},
		{"a developer message after the result", "call_a", "", first, "", followed + `,{"role":"developer","content":"New rule."}`, legReplay},

		// The second round of a conversation: the record is of the call the second leg made.
		{"round two as it was", "call_b", "", followed, "", followed + "," + callRomeMsg + "," + rome25, legResume},
		{"an empty content for a null one", "call_b", "", followed, "", sysBrief + "," + askParis + "," + parisEmpty + "," + paris21 + "," + callRomeMsg + "," + rome25, legResume},
		{"words the client trimmed", "call_b", "", sysBrief + "," + askParis + "," + parisNarrated + "," + paris21, "", sysBrief + "," + askParis + "," + parisTrimmed + "," + paris21 + "," + callRomeMsg + "," + rome25, legResume},
		{"arguments the client spaced out", "call_b", "", followed, "", sysBrief + "," + askParis + "," + parisSpaced + "," + paris21 + "," + callRomeMsg + "," + rome25, legResume},
		{"a question sent as parts", "call_b", "", followed, "", sysBrief + "," + askParisAsParts + "," + callParisMsg + "," + paris21 + "," + callRomeMsg + "," + rome25, legResume},
		{"an earlier call with other arguments", "call_b", "", followed, "", sysBrief + "," + askParis + "," + parisOtherArgs + "," + paris21 + "," + callRomeMsg + "," + rome25, legReplay},
		{"an earlier call that nobody answered has another id", "call_c", "", first + "," + twoCallsMsg + "," + okA, "", first + `,{"role":"assistant","content":null,"tool_calls":[` + callParis + `,` + callRomeZ + `]},` + okA + "," + callOsloMsg + "," + oslo3, legReplay},
		{"an earlier call that nobody answered has another name", "call_c", "", first + "," + twoCallsMsg + "," + okA, "", first + `,{"role":"assistant","content":null,"tool_calls":[` + callParis + `,` + callRomeStock + `]},` + okA + "," + callOsloMsg + "," + oslo3, legReplay},
		{"results of an earlier round in the same order", "call_c", "", first + "," + twoCallsMsg + "," + okA + "," + okB, "", first + "," + twoCallsMsg + "," + okA + "," + okB + "," + callOsloMsg + "," + oslo3, legResume},
		{"results of an earlier round in another order", "call_c", "", first + "," + twoCallsMsg + "," + okA + "," + okB, "", first + "," + twoCallsMsg + "," + okB + "," + okA + "," + callOsloMsg + "," + oslo3, legReplay},
		{"an earlier result edited", "call_b", "", followed, "", first + "," + callParisMsg + `,{"role":"tool","tool_call_id":"call_a","content":"22"}` + "," + callRomeMsg + "," + rome25, legReplay},
	}
	for _, c := range cases {
		got := planAfter(t, c.callID, c.firstExtra, c.firstMsgs, c.followExtra, c.followMsgs)
		if got.Kind != c.want {
			t.Errorf("%s: the plan is a %s, want a %s", c.name, got.Kind, c.want)
		}
		if c.want == legReplay && got.Session != "" {
			t.Errorf("%s: a replay must not carry a session: %+v", c.name, got)
		}
	}
}
