package openaiapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/monomind"
)

// withReadAccess gives a harness the handshake of a monomind that can run runtimes
// read-only, as 2.22 can.
func withReadAccess(d *Deps, _ *Config) {
	d.Catalog.Caps = func(context.Context) (*monomind.CapabilitySet, error) {
		return monomind.NewCapabilitySet("2.22.0", monomind.CapAgentExecSandbox, monomind.CapAgentExecAccessRead), nil
	}
}

// toolHarness is a gateway whose monomind can run runtimes read-only, with a short
// wait for a cancelled runtime to be gone.
func toolHarness(t *testing.T, exec execFunc, mutate ...func(*Deps, *Config)) *harness {
	t.Helper()
	shortGrace(t)
	return newHarness(t, exec, append([]func(*Deps, *Config){withReadAccess}, mutate...)...)
}

func toolChatBody(model, extra, messages string) string {
	if extra != "" {
		extra += ","
	}
	return `{"model":"` + model + `",` + extra + `"messages":[` + messages + `]}`
}

const (
	weatherQuestion = `{"role":"user","content":"What is the weather in Paris?"}`
	weatherTools    = `"tools":[` + weatherTool + `]`
)

// toolReply is a chat completion as a client reads it, whether it ends at a call
// or not.
type toolReply struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Role      string  `json:"role"`
			Content   *string `json:"content"`
			ToolCalls []struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *usage `json:"usage"`
}

func decodeToolReply(t *testing.T, rec *httpRecorder) toolReply {
	t.Helper()
	var r toolReply
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil || len(r.Choices) != 1 {
		t.Fatalf("not a completion (status %d): %s", rec.Code, rec.Body)
	}
	return r
}

// callsWeather is a leg of a runtime that says a few words, calls get_weather and
// waits for its result until the leg cancels it.
func callsWeather(streams bool, session, said string) execFunc {
	return fakeLegExec(func(ctx context.Context, emit func(monomind.Event)) {
		emit(evStart(streams, "monomind"))
		emit(evSession(session))
		if said != "" {
			emit(evText(said))
		}
		emit(evCall("get_weather", `{"city":"Paris"}`))
		<-ctx.Done()
	})
}

func answers(text string) execFunc {
	return scriptedExec(evStart(true, "monomind"), evSession("sess-1"), evText(text), evUsage(5, 6), evResult(text, monomind.StopEndTurn), evDone(0))
}

// followUp is the request a client sends after it ran the call: the question, the
// assistant message that made it and the result.
func followUp(callID, result string) string {
	return toolChatBody("claude", weatherTools, weatherQuestion+
		`,{"role":"assistant","content":null,"tool_calls":[{"id":"`+callID+`","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Paris\"}"}}]},`+
		`{"role":"tool","tool_call_id":"`+callID+`","content":`+jsonString(result)+`}`)
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestToolsALegEndsAtItsCallAndTheFollowUpResumesTheSession(t *testing.T) {
	script := &execScript{turns: []execFunc{callsWeather(true, "sess-1", "Looking it up."), answers("It is 21 C in Paris.")}}
	h := toolHarness(t, script.exec)
	secret := h.key(t, "default", "app", false)

	rec := post(h, anyPolicy, secret, toolChatBody("claude", weatherTools, weatherQuestion))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
	first := decodeToolReply(t, rec)
	msg := first.Choices[0].Message
	if first.Object != "chat.completion" || !strings.HasPrefix(first.ID, "chatcmpl-") || first.Model != "claude/default" {
		t.Errorf("envelope: %+v", first)
	}
	if first.Choices[0].FinishReason != "tool_calls" || msg.Role != "assistant" || msg.Content == nil || *msg.Content != "Looking it up." {
		t.Errorf("message: %+v finish %q", msg, first.Choices[0].FinishReason)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("a leg ends at one call: %+v", msg.ToolCalls)
	}
	call := msg.ToolCalls[0]
	if !strings.HasPrefix(call.ID, "call_") || len(call.ID) < 16 || call.Type != "function" || call.Function.Name != "get_weather" || call.Function.Arguments != `{"city":"Paris"}` {
		t.Errorf("call: %+v", call)
	}
	if first.Usage != nil || strings.Contains(rec.Body.String(), `"usage"`) {
		t.Errorf("a leg that ended at a call promises no usage: %s", rec.Body)
	}
	if rec.Header().Get("X-Request-Id") == "" || rec.Header().Get("X-Monoagent-Model") != "claude/default" {
		t.Errorf("headers: %v", rec.Header())
	}

	opts := script.calls()[0]
	if len(opts.Tools) != 1 || opts.Tools[0].Name != "get_weather" || !strings.Contains(opts.Tools[0].Description, "Parameters (JSON Schema):") {
		t.Errorf("tools: %+v", opts.Tools)
	}
	if opts.OnToolCall == nil || opts.Resume != "" || opts.Prompt != "What is the weather in Paris?" || opts.MaxTurns != toolLegMaxTurns {
		t.Errorf("the first leg's options: resume %q prompt %q max turns %d", opts.Resume, opts.Prompt, opts.MaxTurns)
	}
	if opts.Access != "" || opts.Sandbox != monomind.TurnSandboxMode || len(opts.Settings) != 0 || len(opts.AllowBashPrefixes) != 0 {
		t.Errorf("a claude leg keeps the text turn's posture: access %q sandbox %q", opts.Access, opts.Sandbox)
	}

	// The client ran the call and answers: the follow-up continues the session.
	rec = post(h, anyPolicy, secret, followUp(call.ID, "21 C, fog"))
	if rec.Code != http.StatusOK {
		t.Fatalf("follow-up: status %d, body %s", rec.Code, rec.Body)
	}
	second := decodeToolReply(t, rec)
	if second.Choices[0].FinishReason != "stop" || second.Choices[0].Message.Content == nil || *second.Choices[0].Message.Content != "It is 21 C in Paris." ||
		len(second.Choices[0].Message.ToolCalls) != 0 {
		t.Errorf("answer: %+v", second.Choices[0])
	}
	if second.Usage == nil || second.Usage.PromptTokens != 5 || second.Usage.CompletionTokens != 6 {
		t.Errorf("an answer carries usage as chat does: %+v", second.Usage)
	}
	resumed := script.calls()[1]
	if resumed.Resume != "sess-1" || len(resumed.Tools) != 1 {
		t.Errorf("the follow-up must continue the session with the tools declared: resume %q tools %d", resumed.Resume, len(resumed.Tools))
	}
	if !strings.Contains(resumed.Prompt, "Result of get_weather (call "+call.ID+"):\n"+fenced("21 C, fog")) || strings.Contains(resumed.Prompt, "What is the weather") {
		t.Errorf("the resumed prompt is the result and nothing the session already holds: %q", resumed.Prompt)
	}
}

func TestToolsAFollowUpWithoutARecordReplaysTheTranscript(t *testing.T) {
	script := &execScript{turns: []execFunc{answers("It is 21 C.")}}
	h := toolHarness(t, script.exec)
	rec := post(h, anyPolicy, h.key(t, "default", "app", false), followUp("call_from_another_server", "21 C"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
	opts := script.calls()[0]
	if opts.Resume != "" || len(opts.Tools) != 1 {
		t.Errorf("a replay starts a session of its own and declares the tools again: resume %q tools %d", opts.Resume, len(opts.Tools))
	}
	for _, want := range []string{"[user]\nWhat is the weather in Paris?", "(called the function get_weather with arguments {\"city\":\"Paris\"})", "[tool get_weather (call_from_another_server)]\n" + fenced("21 C"), toolOutro} {
		if !strings.Contains(opts.Prompt, want) {
			t.Errorf("the transcript lacks %q:\n%s", want, opts.Prompt)
		}
	}
}

func TestToolsAResumeTheRuntimeCannotContinueFallsBackToAReplay(t *testing.T) {
	script := &execScript{turns: []execFunc{unknownSession(), answers("It is 21 C.")}}
	h := toolHarness(t, script.exec)
	req := toolRequest(t, weatherTools, weatherQuestion)
	secret := h.key(t, "default", "app", false)
	h.g.conts.put(contRecord{CallID: "call_x", KeyID: keyIDOf(t, h, secret), ProfileID: "default", Model: "claude/default", Name: "get_weather",
		Session: "sess-gone", ToolsHash: toolsHash(req.toolDecls), Convo: convoHash(req, len(req.Messages))})

	rec := post(h, anyPolicy, secret, followUp("call_x", "21 C"))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "It is 21 C.") {
		t.Fatalf("the caller must never learn that the session was gone: %d %s", rec.Code, rec.Body)
	}
	calls := script.calls()
	if len(calls) != 2 || calls[0].Resume != "sess-gone" || calls[1].Resume != "" || !strings.Contains(calls[1].Prompt, "[tool get_weather (call_x)]") {
		t.Fatalf("Exec calls: %+v", calls)
	}
	if line := logLineOf(h, rec); !strings.Contains(line, "leg=replay") {
		t.Errorf("the log line says how the leg that answered started: %q", line)
	}
}

// A client that changes its system prompt between the rounds of a conversation gets
// its follow-up served from the transcript, with the new system prompt: a resumed
// session would go on with the one it was started with.
func TestToolsAFollowUpWithAnotherSystemPromptIsReplayed(t *testing.T) {
	script := &execScript{turns: []execFunc{callsWeather(true, "sess-1", ""), answers("Il fait 21 C."), answers("It is 21 C.")}}
	h := toolHarness(t, script.exec)
	secret := h.key(t, "default", "app", false)
	const brief, french = `{"role":"system","content":"Be brief."},`, `{"role":"system","content":"Answer in French."},`

	rec := post(h, anyPolicy, secret, toolChatBody("claude", weatherTools, brief+weatherQuestion))
	call := decodeToolReply(t, rec).Choices[0].Message.ToolCalls[0]

	rec = post(h, anyPolicy, secret, strings.Replace(followUp(call.ID, "21 C"), weatherQuestion, french+weatherQuestion, 1))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
	replayed := script.calls()[1]
	if replayed.Resume != "" || !strings.Contains(replayed.SystemPrompt, "Answer in French.") || strings.Contains(replayed.SystemPrompt, "Be brief.") || !strings.Contains(replayed.Prompt, "[tool get_weather ("+call.ID+")]") {
		t.Errorf("a follow-up whose system prompt changed must start from the transcript: resume %q system %q prompt %q", replayed.Resume, replayed.SystemPrompt, replayed.Prompt)
	}
	if line := logLineOf(h, rec); !strings.Contains(line, "leg=replay") {
		t.Errorf("the log line says how the leg that answered started: %q", line)
	}

	// The record was not used up by a follow-up that was not the conversation's own:
	// the client that sends the conversation as it was still resumes the session.
	rec = post(h, anyPolicy, secret, strings.Replace(followUp(call.ID, "21 C"), weatherQuestion, brief+weatherQuestion, 1))
	if rec.Code != http.StatusOK || script.calls()[2].Resume != "sess-1" {
		t.Errorf("the owner of the conversation lost its session: %d resume %q", rec.Code, script.calls()[2].Resume)
	}
}

// keyIDOf is the id of the key a secret belongs to.
func keyIDOf(t *testing.T, h *harness, secret string) string {
	t.Helper()
	key, err := h.keys.Authenticate(context.Background(), secret)
	if err != nil {
		t.Fatal(err)
	}
	return key.ID
}

// logLineOf is the line the gateway logged for a response's request.
func logLineOf(h *harness, rec *httpRecorder) string {
	id := rec.Header().Get("X-Request-Id")
	for _, l := range h.logged() {
		if strings.Contains(l, "req="+id) {
			return l
		}
	}
	return ""
}

func TestToolsARunnerThatAnswersWithoutCallingIsAnOrdinaryAnswer(t *testing.T) {
	script := &execScript{turns: []execFunc{answers("42")}}
	h := toolHarness(t, script.exec)
	rec := post(h, anyPolicy, h.key(t, "default", "app", false), toolChatBody("claude", weatherTools, `{"role":"user","content":"What is 17 plus 25?"}`))
	got := decodeToolReply(t, rec)
	if rec.Code != 200 || got.Choices[0].FinishReason != "stop" || *got.Choices[0].Message.Content != "42" || len(got.Choices[0].Message.ToolCalls) != 0 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if h.g.conts.size() != 0 {
		t.Error("an answer leaves no record")
	}
}

func TestToolsALegThatHitsItsTurnCapIsALengthFinish(t *testing.T) {
	h := toolHarness(t, scriptedExec(evStart(true, "monomind"), evSession("s"), evText("partial"), evResult("partial", monomind.StopMaxTurns), evDone(0)))
	got := decodeToolReply(t, post(h, anyPolicy, h.key(t, "default", "app", false), toolChatBody("claude", weatherTools, weatherQuestion)))
	if got.Choices[0].FinishReason != "length" || *got.Choices[0].Message.Content != "partial" {
		t.Errorf("answer: %+v", got.Choices[0])
	}
}

func TestToolsParallelCallsAreAnsweredOneAtATime(t *testing.T) {
	twoCalls := fakeLegExec(func(ctx context.Context, emit func(monomind.Event)) {
		emit(evStart(false, "workspace-write"))
		emit(evSession("s"))
		emit(evCall("get_weather", `{"city":"Paris"}`))
		emit(evCall("get_weather", `{"city":"Tokyo"}`))
		<-ctx.Done()
	})
	for _, extra := range []string{weatherTools, weatherTools + `,"parallel_tool_calls":false`, weatherTools + `,"parallel_tool_calls":true`} {
		h := toolHarness(t, twoCalls)
		got := decodeToolReply(t, post(h, anyPolicy, h.key(t, "default", "app", false), toolChatBody("codex", extra, weatherQuestion)))
		calls := got.Choices[0].Message.ToolCalls
		if len(calls) != 1 || calls[0].Function.Arguments != `{"city":"Paris"}` {
			t.Errorf("%s: a response carries the first call only: %+v", extra, calls)
		}
	}
}

// A non-streaming response says how monomind sandboxed the leg, as chat's does.
func TestToolsSandboxStatusHeader(t *testing.T) {
	withStatus := func(inner execFunc) execFunc {
		return func(ctx context.Context, o monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
			res, err := inner(ctx, o, onEvent)
			if res != nil {
				res.SandboxStatus = monomind.SandboxStatusSandboxed
			}
			return res, err
		}
	}
	for name, exec := range map[string]execFunc{"a call": withStatus(callsWeather(false, "s", "")), "an answer": withStatus(answers("x"))} {
		h := toolHarness(t, exec)
		rec := post(h, anyPolicy, h.key(t, "default", "app", false), toolChatBody("codex/gpt-6-astra", weatherTools, weatherQuestion))
		if rec.Code != 200 || rec.Header().Get("X-Monoagent-Sandbox") != "sandboxed" {
			t.Errorf("%s: status %d, X-Monoagent-Sandbox = %q", name, rec.Code, rec.Header().Get("X-Monoagent-Sandbox"))
		}
	}
}

func TestToolsContentIsNullWhenTheModelSaidNothingBeforeTheCall(t *testing.T) {
	h := toolHarness(t, callsWeather(true, "s", ""))
	rec := post(h, anyPolicy, h.key(t, "default", "app", false), toolChatBody("claude", weatherTools, weatherQuestion))
	if !strings.Contains(rec.Body.String(), `"content":null`) || decodeToolReply(t, rec).Choices[0].Message.Content != nil {
		t.Errorf("an assistant message with calls and no words has null content: %s", rec.Body)
	}
}

func TestToolsChoiceLinesReachTheSystemPrompt(t *testing.T) {
	cases := map[string]string{
		`"tool_choice":"required"`: "You must call at least one of the available functions before you answer.",
		`"tool_choice":{"type":"function","function":{"name":"get_weather"}}`: "You must call the function get_weather before you answer.",
		`"tool_choice":"auto"`: "",
	}
	for choice, line := range cases {
		script := &execScript{turns: []execFunc{answers("x")}}
		h := toolHarness(t, script.exec)
		post(h, anyPolicy, h.key(t, "default", "app", false), toolChatBody("claude", weatherTools+","+choice, `{"role":"system","content":"Be brief."},`+weatherQuestion))
		sys := script.calls()[0].SystemPrompt
		if !strings.HasPrefix(sys, "Be brief.") || (line != "" && !strings.Contains(sys, line)) || (line == "" && sys != "Be brief.") {
			t.Errorf("%s: system prompt %q", choice, sys)
		}
	}
}

// tool_choice none passes no tools: the turn is a plain one, and the history of
// an earlier round is only text in its transcript.
func TestToolsChoiceNonePassesNoTools(t *testing.T) {
	script := &execScript{turns: []execFunc{answers("It was 21 C.")}}
	h := toolHarness(t, script.exec)
	rec := post(h, anyPolicy, h.key(t, "default", "app", false), strings.Replace(followUp("call_1", "21 C"), weatherTools, weatherTools+`,"tool_choice":"none"`, 1))
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	opts := script.calls()[0]
	if len(opts.Tools) != 0 || opts.OnToolCall != nil || opts.Resume != "" || opts.MaxTurns != 0 || opts.Access != "" {
		t.Errorf("a turn without tools carries none of a leg's options: %+v", opts)
	}
	if !strings.Contains(opts.Prompt, "[tool get_weather (call_1)]\n"+fenced("21 C")) || !strings.HasSuffix(opts.Prompt, plainToolOutro) {
		t.Errorf("the history is rendered for a turn that cannot call:\n%s", opts.Prompt)
	}
}

// A conversation that carries tool history but declares no tools (a client that
// dropped them for its last round) is a plain turn with the history as text.
func TestToolsHistoryWithoutToolsIsATranscript(t *testing.T) {
	script := &execScript{turns: []execFunc{answers("x")}}
	h := toolHarness(t, script.exec)
	rec := post(h, anyPolicy, h.key(t, "default", "app", false), strings.Replace(followUp("call_1", "21 C"), weatherTools+",", "", 1))
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	opts := script.calls()[0]
	if len(opts.Tools) != 0 || !strings.Contains(opts.Prompt, "[tool get_weather (call_1)]") {
		t.Errorf("tools %d prompt %q", len(opts.Tools), opts.Prompt)
	}
}

func TestToolsACodexLegRunsReadOnlyAndItsStartEventIsAccepted(t *testing.T) {
	// monomind 2.22's start event for access read and the workspace-write sandbox.
	const start = `{"v":1,"type":"start","runtime":"codex","cwd":"/x/slot-0","pid":4242,"access":"read","native_sandbox":"read-only","approvals":"off","sandbox_requested":"workspace-write","sandbox_applied":"workspace-write","streams_incrementally":false}`
	var startEv monomind.Event
	if err := json.Unmarshal([]byte(start), &startEv); err != nil {
		t.Fatal(err)
	}
	var opts monomind.ExecOptions
	h := toolHarness(t, fakeLegExec(func(ctx context.Context, emit func(monomind.Event)) {
		emit(startEv)
		emit(evSession("thread-1"))
		emit(evCall("get_weather", `{"city":"Paris"}`))
		<-ctx.Done()
	}))
	inner := h.g.deps.Exec
	h.g.deps.Exec = func(ctx context.Context, o monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		opts = o
		return inner(ctx, o, onEvent)
	}
	rec := post(h, Policy{Max: Sandboxed}, h.key(t, "default", "app", false), toolChatBody("codex/gpt-6-astra", weatherTools, weatherQuestion))
	if rec.Code != 200 || len(decodeToolReply(t, rec).Choices[0].Message.ToolCalls) != 1 {
		t.Fatalf("a read-only codex leg was refused or lost its call: %d %s", rec.Code, rec.Body)
	}
	if opts.Access != monomind.AccessRead || !opts.RequireSandbox || opts.Sandbox != monomind.TurnSandboxMode {
		t.Errorf("access %q require sandbox %v sandbox %q: a codex leg runs read-only, still with its sandbox required", opts.Access, opts.RequireSandbox, opts.Sandbox)
	}
}

func TestToolsASandboxThatCannotBeAppliedStillRefusesTheLeg(t *testing.T) {
	h := toolHarness(t, func(context.Context, monomind.ExecOptions, func(monomind.Event)) (*monomind.TurnResult, error) {
		return nil, fmt.Errorf("%w: workspace-write sandbox for codex is unsupported", monomind.ErrSandboxRequired)
	})
	rec := post(h, anyPolicy, h.key(t, "default", "app", false), toolChatBody("codex/gpt-6-astra", weatherTools, weatherQuestion))
	if rec.Code != http.StatusForbidden || decodeErrorBody(t, rec)["code"] != "policy_denied" {
		t.Fatalf("a leg whose sandbox cannot be applied must be refused, not run unconfined: %d %s", rec.Code, rec.Body)
	}
}
