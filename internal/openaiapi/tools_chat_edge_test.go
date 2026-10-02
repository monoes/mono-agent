package openaiapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/monomind"
)

func TestToolsAreRefusedOnRuntimesThatDoNotServeThem(t *testing.T) {
	var spawned atomic.Int32
	h := toolHarness(t, func(ctx context.Context, o monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		spawned.Add(1)
		return okTurn("x")(ctx, o, onEvent)
	})
	secret := h.key(t, "default", "app", false)
	rec := post(h, anyPolicy, secret, toolChatBody("antigravity", weatherTools, weatherQuestion))
	e := decodeErrorBody(t, rec)
	if rec.Code != http.StatusBadRequest || e["code"] != "unsupported_parameter" || e["param"] != "tools" {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	for _, want := range []string{"claude", "codex", "MONOAGENT_API_TOOL_RUNTIMES"} {
		if !strings.Contains(e["message"].(string), want) {
			t.Errorf("the message does not say %q: %v", want, e["message"])
		}
	}
	// Without tools the same model answers.
	if rec := post(h, anyPolicy, secret, toolChatBody("antigravity", "", weatherQuestion)); rec.Code != 200 {
		t.Errorf("plain chat on antigravity: %d %s", rec.Code, rec.Body)
	}
	if spawned.Load() != 1 {
		t.Errorf("%d turns started: only the plain chat may start one", spawned.Load())
	}
}

func TestToolsAreRefusedWhereMonomindCannotRunTheRuntimeReadOnly(t *testing.T) {
	// monomind without agent-exec-access-read: codex's own tools could not be kept
	// out of the way (the spike: the declared tool was used 0 of 4 times), claude's
	// are gated by monomind and need nothing.
	var spawned atomic.Int32
	h := newHarness(t, func(ctx context.Context, o monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		spawned.Add(1)
		return answers("x")(ctx, o, onEvent)
	})
	secret := h.key(t, "default", "app", false)
	rec := post(h, anyPolicy, secret, toolChatBody("codex/gpt-6-astra", weatherTools, weatherQuestion))
	if rec.Code != http.StatusBadRequest || decodeErrorBody(t, rec)["code"] != "unsupported_parameter" || spawned.Load() != 0 {
		t.Fatalf("codex: status %d, %d turns: %s", rec.Code, spawned.Load(), rec.Body)
	}
	if rec := post(h, anyPolicy, secret, toolChatBody("claude", weatherTools, weatherQuestion)); rec.Code != 200 {
		t.Errorf("claude: %d %s", rec.Code, rec.Body)
	}
}

func TestToolsServedRuntimesAreTheOperatorsList(t *testing.T) {
	h := toolHarness(t, answers("x"), func(_ *Deps, c *Config) { c.ToolRuntimes = []string{"codex"} })
	secret := h.key(t, "default", "app", false)
	if rec := post(h, anyPolicy, secret, toolChatBody("claude", weatherTools, weatherQuestion)); rec.Code != http.StatusBadRequest {
		t.Errorf("claude is not in the list: %d %s", rec.Code, rec.Body)
	}
	if rec := post(h, anyPolicy, secret, toolChatBody("codex/gpt-6-astra", weatherTools, weatherQuestion)); rec.Code != 200 {
		t.Errorf("codex is: %d %s", rec.Code, rec.Body)
	}
}

// A request that is refused starts nothing: not the knowledge search of a context
// key (two monomind processes), not a turn, and it is answered before a busy
// server would answer it with a 429.
func TestToolsAreRefusedBeforeAnythingStarts(t *testing.T) {
	var searched, spawned atomic.Int32
	release := make(chan struct{})
	h := toolHarness(t, func(ctx context.Context, o monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		spawned.Add(1)
		<-release
		return okTurn("x")(ctx, o, onEvent)
	}, func(d *Deps, c *Config) {
		c.MaxConcurrent = 1
		d.Knowledge = func(context.Context, string, string) ([]monomind.KnowledgeResult, error) {
			searched.Add(1)
			return []monomind.KnowledgeResult{{Path: "/a/b.md", Excerpt: "e", Score: 1}}, nil
		}
	})
	t.Cleanup(func() { close(release) })
	ctxKey := h.key(t, "default", "ctx", true)
	plain := h.key(t, "default", "plain", false)
	policy := Policy{Max: Unconfined, ContextMax: Unconfined} // a context key may use antigravity here

	// The one slot is taken by a turn that waits.
	busy := make(chan *httpRecorder, 1)
	go func() { busy <- post(h, policy, plain, toolChatBody("claude", "", weatherQuestion)) }()
	for spawned.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	searched.Store(0)

	rec := post(h, policy, ctxKey, toolChatBody("antigravity", weatherTools, weatherQuestion))
	if rec.Code != http.StatusBadRequest || decodeErrorBody(t, rec)["code"] != "unsupported_parameter" {
		t.Errorf("status %d: %s (a refusal comes before the 429 of a busy server)", rec.Code, rec.Body)
	}
	if searched.Load() != 0 || spawned.Load() != 1 {
		t.Errorf("the knowledge was searched %d times and %d turns started for a request that had to be refused", searched.Load(), spawned.Load())
	}
}

// What the key's policy forbids is a 403 before anything about tools.
func TestToolsThePolicyComesBeforeTheToolRefusal(t *testing.T) {
	h := toolHarness(t, answers("x"))
	rec := post(h, Policy{Max: ChatOnly}, h.key(t, "default", "app", false), toolChatBody("codex/gpt-6-astra", weatherTools, weatherQuestion))
	if rec.Code != http.StatusForbidden || decodeErrorBody(t, rec)["code"] != "policy_denied" {
		t.Errorf("status %d: %s", rec.Code, rec.Body)
	}
}

func TestToolsLogLinesCarryCountsAndNeverNamesArgumentsOrResults(t *testing.T) {
	const marker = "ZQX"
	tool := `{"type":"function","function":{"name":"get_weather","description":"` + marker + `DESC","parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}}`
	call := fakeLegExec(func(ctx context.Context, emit func(monomind.Event)) {
		emit(evStart(true, "monomind"))
		emit(evSession("sess-1"))
		emit(evText(marker + "NARRATION"))
		emit(evCall("get_weather", `{"city":"`+marker+`CITY"}`))
		<-ctx.Done()
	})
	failing := scriptedExec(evStart(true, "monomind"), evSession("sess-1"), evText("half"), evError(monomind.ErrRunnerError, "boom "+marker+"RUNTIMEWORDS"), evDone(1))
	script := &execScript{turns: []execFunc{call, answers("fine"), call, failing}}
	h := toolHarness(t, script.exec)
	secret := h.key(t, "default", "app", false)
	sys := `{"role":"system","content":"` + marker + `SYSTEM"},`
	body1 := toolChatBody("claude", `"tools":[`+tool+`]`, sys+`{"role":"user","content":"`+marker+`QUESTION"}`)

	rec1 := post(h, anyPolicy, secret, body1)
	callID := decodeToolReply(t, rec1).Choices[0].Message.ToolCalls[0].ID
	follow := func() string {
		return toolChatBody("claude", `"tools":[`+tool+`]`, sys+`{"role":"user","content":"`+marker+`QUESTION"},`+
			`{"role":"assistant","content":null,"tool_calls":[{"id":"`+callID+`","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"`+marker+`CITY\"}"}}]},`+
			`{"role":"tool","tool_call_id":"`+callID+`","content":"`+marker+`RESULT"}`)
	}
	rec2 := post(h, anyPolicy, secret, follow())
	// A second conversation whose resume ends in a runner error after the model spoke.
	rec3 := post(h, anyPolicy, secret, body1)
	callID = decodeToolReply(t, rec3).Choices[0].Message.ToolCalls[0].ID
	rec4 := post(h, anyPolicy, secret, follow())

	lines := h.logged()
	for _, l := range lines {
		if strings.Contains(l, marker) {
			t.Errorf("a log line carries a name, an argument, a result or a prompt: %q", l)
		}
	}
	for rec, want := range map[*httpRecorder][]string{
		rec1: {"status=200", "tools=1", "leg=first"},
		rec2: {"status=200", "tools=1", "leg=resume"},
		rec3: {"status=200", "tools=1", "leg=first"},
	} {
		line := logLineOf(h, rec)
		for _, w := range want {
			if !strings.Contains(line, w) {
				t.Errorf("the line %q lacks %q", line, w)
			}
		}
	}
	if rec4.Code != http.StatusBadGateway || strings.Contains(rec4.Body.String(), marker) {
		t.Errorf("an error to the caller never echoes a result or the runtime's words: %d %s", rec4.Code, rec4.Body)
	}
	if line := logLineOf(h, rec4); !strings.Contains(line, "status=502") || !strings.Contains(line, "leg=resume") {
		t.Errorf("failed leg: %q", line)
	}
}

func TestToolsALogLineCountsACallThatDoesNotMatchItsSchema(t *testing.T) {
	bad := fakeLegExec(func(ctx context.Context, emit func(monomind.Event)) {
		emit(evStart(true, "monomind"))
		emit(evSession("s"))
		emit(evCall("get_weather", `{"town":"Paris"}`)) // the required property is city
		<-ctx.Done()
	})
	h := toolHarness(t, bad)
	rec := post(h, anyPolicy, h.key(t, "default", "app", false), toolChatBody("claude", weatherTools, weatherQuestion))
	// The call is returned all the same: the client decides.
	got := decodeToolReply(t, rec)
	if rec.Code != 200 || len(got.Choices[0].Message.ToolCalls) != 1 || got.Choices[0].Message.ToolCalls[0].Function.Arguments != `{"town":"Paris"}` {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if line := logLineOf(h, rec); !strings.Contains(line, "badargs=1") || strings.Contains(line, "town") {
		t.Errorf("log line: %q", line)
	}
	// A call that matches counts for nothing.
	h = toolHarness(t, callsWeather(true, "s", ""))
	rec = post(h, anyPolicy, h.key(t, "default", "app", false), toolChatBody("claude", weatherTools, weatherQuestion))
	if line := logLineOf(h, rec); strings.Contains(line, "badargs") {
		t.Errorf("log line: %q", line)
	}
}

// A record belongs to its key: another key, even of the same profile, replays.
func TestToolsRecordsBelongToTheirKey(t *testing.T) {
	script := &execScript{turns: []execFunc{callsWeather(true, "sess-a", ""), answers("x"), answers("y"), answers("z")}}
	h := toolHarness(t, script.exec)
	owner := h.key(t, "default", "owner", false)
	stranger := h.key(t, "default", "stranger", false)
	otherProfile := h.key(t, "bob", "bobs", false)

	callID := decodeToolReply(t, post(h, anyPolicy, owner, toolChatBody("claude", weatherTools, weatherQuestion))).Choices[0].Message.ToolCalls[0].ID
	for name, secret := range map[string]string{"another key of the profile": stranger, "another profile": otherProfile} {
		before := len(script.calls())
		if rec := post(h, anyPolicy, secret, followUp(callID, "21 C")); rec.Code != 200 {
			t.Fatalf("%s: %d %s", name, rec.Code, rec.Body)
		}
		if got := script.calls()[before]; got.Resume != "" {
			t.Errorf("%s resumed the owner's session %q", name, got.Resume)
		}
	}
	// The strangers' attempts did not use the record up.
	before := len(script.calls())
	if rec := post(h, anyPolicy, owner, followUp(callID, "21 C")); rec.Code != 200 {
		t.Fatalf("owner: %d %s", rec.Code, rec.Body)
	}
	if got := script.calls()[before]; got.Resume != "sess-a" {
		t.Errorf("the owner's follow-up resumed %q", got.Resume)
	}
}

func TestToolsARevokedKeyReachesNoRecord(t *testing.T) {
	script := &execScript{turns: []execFunc{callsWeather(true, "sess-a", ""), answers("x")}}
	h := toolHarness(t, script.exec)
	secret := h.key(t, "default", "app", false)
	callID := decodeToolReply(t, post(h, anyPolicy, secret, toolChatBody("claude", weatherTools, weatherQuestion))).Choices[0].Message.ToolCalls[0].ID
	if _, err := h.keys.Revoke(context.Background(), "default", "app"); err != nil {
		t.Fatal(err)
	}
	if rec := post(h, anyPolicy, secret, followUp(callID, "21 C")); rec.Code != http.StatusUnauthorized {
		t.Errorf("a revoked key: %d %s", rec.Code, rec.Body)
	}
	fresh := h.key(t, "default", "again", false)
	if rec := post(h, anyPolicy, fresh, followUp(callID, "21 C")); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if got := script.calls()[1]; got.Resume != "" {
		t.Errorf("a new key resumed a revoked key's session %q", got.Resume)
	}
}

func TestToolsAClientThatLeavesEndsItsLegAndLeavesNoRecord(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	h := toolHarness(t, fakeLegExec(func(c context.Context, emit func(monomind.Event)) {
		emit(evStart(true, "monomind"))
		emit(evSession("s"))
		cancel() // the caller hangs up while the model is calling its tool
		emit(evCall("get_weather", `{"city":"Paris"}`))
		<-c.Done()
	}))
	secret := h.key(t, "default", "app", false)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "/v1/chat/completions", strings.NewReader(toolChatBody("claude", weatherTools, weatherQuestion)))
	req.Header.Set("Authorization", "Bearer "+secret)
	done := make(chan *httpRecorder, 1)
	go func() { done <- h.do(anyPolicy, req) }()
	select {
	case rec := <-done:
		if rec.Body.Len() != 0 {
			t.Errorf("a response was written to a caller that left: %s", rec.Body)
		}
		if line := logLineOf(h, rec); !strings.Contains(line, "status=499") {
			t.Errorf("log line: %q", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the handler did not return after the caller left")
	}
	if h.g.conts.size() != 0 {
		t.Error("a record was kept for a response nobody received")
	}
}

func TestToolsALegCutShortByShutdownIsA503(t *testing.T) {
	started := make(chan struct{})
	h := toolHarness(t, fakeLegExec(func(ctx context.Context, emit func(monomind.Event)) {
		emit(evStart(true, "monomind"))
		close(started)
		<-ctx.Done() // a runtime still thinking
	}))
	secret := h.key(t, "default", "app", false)
	got := make(chan *httpRecorder, 1)
	go func() { got <- post(h, anyPolicy, secret, toolChatBody("claude", weatherTools, weatherQuestion)) }()
	<-started
	h.g.Shutdown(5 * time.Second)
	select {
	case rec := <-got:
		if rec.Code != http.StatusServiceUnavailable || decodeErrorBody(t, rec)["code"] != "runtime_not_available" {
			t.Fatalf("status %d: %s", rec.Code, rec.Body)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the handler did not return after Shutdown")
	}
}

// A leg holds a slot while it runs and not after: no process waits for the result.
func TestToolsALegFreesItsSlotAndLeavesItsFolderEmpty(t *testing.T) {
	var cwds []string
	h := toolHarness(t, func(ctx context.Context, o monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		cwds = append(cwds, o.Cwd)
		return callsWeather(true, "s", "")(ctx, o, onEvent)
	}, func(_ *Deps, c *Config) { c.MaxConcurrent = 1 })
	secret := h.key(t, "default", "app", false)
	for i := range 3 {
		if rec := post(h, anyPolicy, secret, toolChatBody("claude", weatherTools, weatherQuestion)); rec.Code != 200 {
			t.Fatalf("leg %d: %d %s", i, rec.Code, rec.Body)
		}
	}
	for _, cwd := range cwds {
		if left := besidesTmp(cwd); len(left) != 0 {
			t.Errorf("the slot folder %s holds %v after its leg", cwd, left)
		}
	}
}

// A client loop as an OpenAI SDK runs it, over a real connection: the assistant
// message of the first response goes back as it came, with the result.
func TestToolsAClientLoopOverARealConnection(t *testing.T) {
	script := &execScript{turns: []execFunc{callsWeather(true, "sess-1", "One moment."), answers("It is 21 C in Paris.")}}
	h := toolHarness(t, script.exec)
	srv := httptest.NewServer(h.g.Handler(anyPolicy))
	defer srv.Close()
	secret := h.key(t, "default", "app", false)

	send := func(body string) []byte {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+secret)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 {
			t.Fatalf("status %d: %s", resp.StatusCode, b)
		}
		return b
	}
	var first struct {
		Choices []struct {
			Message json.RawMessage `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(send(toolChatBody("claude", weatherTools, weatherQuestion)), &first); err != nil || len(first.Choices) != 1 {
		t.Fatalf("first response: %v", err)
	}
	var assistant struct {
		ToolCalls []struct {
			ID string `json:"id"`
		} `json:"tool_calls"`
	}
	_ = json.Unmarshal(first.Choices[0].Message, &assistant)
	if len(assistant.ToolCalls) != 1 {
		t.Fatalf("assistant message: %s", first.Choices[0].Message)
	}

	// The message goes back exactly as it came.
	var buf bytes.Buffer
	buf.WriteString(toolChatBody("claude", weatherTools, weatherQuestion+","+string(first.Choices[0].Message)+
		`,{"role":"tool","tool_call_id":"`+assistant.ToolCalls[0].ID+`","content":"21 C"}`))
	var second toolReply
	if err := json.Unmarshal(send(buf.String()), &second); err != nil || *second.Choices[0].Message.Content != "It is 21 C in Paris." {
		t.Fatalf("second response: %+v %v", second, err)
	}
	if got := script.calls()[1]; got.Resume != "sess-1" {
		t.Errorf("the message went back as it came and must find its record: resume %q", got.Resume)
	}
}

// firstCall runs a first request and returns the id of the call it ended at.
func firstCall(t *testing.T, h *harness, secret string) string {
	t.Helper()
	rec := post(h, anyPolicy, secret, toolChatBody("claude", weatherTools, weatherQuestion))
	reply := decodeToolReply(t, rec)
	if len(reply.Choices[0].Message.ToolCalls) != 1 {
		t.Fatalf("the first request did not end at a call: %s", rec.Body)
	}
	return reply.Choices[0].Message.ToolCalls[0].ID
}

// A resume that ended before the model ran, because the runtime was rate limited,
// out of quota or not signed in, left the session as it was: the client's retry
// continues it instead of paying for a replay of the whole transcript.
func TestToolsAResumeThatFailedBeforeTheModelRanKeepsItsSession(t *testing.T) {
	for name, fail := range map[string]execFunc{
		"rate limited":  scriptedExec(evStart(false, "monomind"), evError(monomind.ErrRateLimited, "slow down"), evDone(1)),
		"out of quota":  scriptedExec(evStart(false, "monomind"), evError(monomind.ErrQuota, "usage limit"), evDone(1)),
		"not signed in": scriptedExec(evStart(false, "monomind"), evError(monomind.ErrAuth, "please sign in"), evDone(1)),
		"over budget":   scriptedExec(evStart(false, "monomind"), evError(monomind.ErrBudget, "budget used up"), evDone(1)),
	} {
		for _, stream := range []bool{false, true} {
			script := &execScript{turns: []execFunc{callsWeather(true, "sess-1", ""), fail, answers("It is 21 C.")}}
			h := toolHarness(t, script.exec)
			secret := h.key(t, "default", "app", false)
			body := followUp(firstCall(t, h, secret), "21 C")
			if stream {
				body = strings.Replace(body, `"model":"claude"`, `"model":"claude","stream":true`, 1)
			}

			if rec := post(h, anyPolicy, secret, body); rec.Code == http.StatusOK && !strings.Contains(rec.Body.String(), `"error"`) {
				t.Fatalf("%s (stream %v): the follow-up should have failed: %s", name, stream, rec.Body)
			}
			rec := post(h, anyPolicy, secret, body)
			calls := script.calls()
			if rec.Code != http.StatusOK || len(calls) != 3 || calls[2].Resume != "sess-1" {
				t.Errorf("%s (stream %v): the retry must continue the session: status %d, %d turns", name, stream, rec.Code, len(calls))
			}
		}
	}
}

// deadlineRecorder is a response recorder that takes a write deadline as net/http's
// real writer does, and keeps the last one it was given.
type deadlineRecorder struct {
	*httptest.ResponseRecorder
	deadline time.Time
}

func (d *deadlineRecorder) SetWriteDeadline(t time.Time) error {
	d.deadline = t
	return nil
}

// serveTimed posts a chat request and says how long after its start the response
// was given to be written.
func serveTimed(h *harness, secret, body string) (*deadlineRecorder, time.Duration) {
	mux := http.NewServeMux()
	h.g.Mount(mux, anyPolicy)
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+secret)
	rec := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	begin := time.Now()
	mux.ServeHTTP(rec, r)
	return rec, rec.deadline.Sub(begin)
}

// A resume the runtime cannot continue is run again from the transcript in the same
// request: a second turn. The response has the time of both, so that the answer of the
// second is not lost to the deadline set for the first.
func TestToolsAResumeIsGivenTheTimeOfTwoTurns(t *testing.T) {
	script := &execScript{turns: []execFunc{callsWeather(true, "sess-1", ""), answers("It is 21 C.")}}
	h := toolHarness(t, script.exec)
	secret := h.key(t, "default", "app", false)

	rec, first := serveTimed(h, secret, toolChatBody("claude", weatherTools, weatherQuestion))
	id := decodeToolReply(t, rec.ResponseRecorder).Choices[0].Message.ToolCalls[0].ID
	_, resumed := serveTimed(h, secret, followUp(id, "21 C"))

	turn := h.g.cfg.TurnTimeout
	if first < turn || first > turn+3*turnGrace {
		t.Errorf("a first leg is one turn: its response was given %v", first)
	}
	if resumed < first+turn {
		t.Errorf("a resume may be followed by a replay, a second turn: its response was given %v, a first leg's %v", resumed, first)
	}
}

// codex repeats an identical call two or three times in a leg that ends at a call (the
// spike saw an edit written twice). A leg ends at the first: the repeats, which arrive
// while the leg is being cancelled, start no second call, no second record and no
// result.
func TestToolsAnIdenticalCallRepeatedWhileTheLegEndsIsOneCallAndOneRecord(t *testing.T) {
	repeats := func() execFunc {
		return fakeLegExec(func(ctx context.Context, emit func(monomind.Event)) {
			emit(evStart(false, "workspace-write"))
			emit(evSession("th_1"))
			for range 3 {
				emit(evCall("get_weather", `{"city":"Paris"}`))
			}
			<-ctx.Done()
		})
	}
	for _, stream := range []bool{false, true} {
		h := toolHarness(t, repeats())
		secret := h.key(t, "default", "app", false)
		extra := weatherTools
		if stream {
			extra += `,"stream":true`
		}
		rec := post(h, anyPolicy, secret, toolChatBody("codex/gpt-6-astra", extra, weatherQuestion))
		calls := 0
		if stream {
			data, _ := sseEvents(rec.Body.String())
			for _, c := range decodeRawChunks(t, data) {
				if raw, ok := c.Choices[0].Delta["tool_calls"]; ok && strings.Contains(string(raw), `"id"`) {
					calls++
				}
			}
		} else {
			calls = len(decodeToolReply(t, rec).Choices[0].Message.ToolCalls)
		}
		if rec.Code != http.StatusOK || calls != 1 || h.g.conts.size() != 1 {
			t.Errorf("stream %v: status %d, %d calls, %d records: %s", stream, rec.Code, calls, h.g.conts.size(), rec.Body)
		}
	}
}

// A first leg that hit the quota has no session to keep and no record to give back.
func TestToolsAFirstLegThatHitTheQuotaIsATooManyRequests(t *testing.T) {
	quota := scriptedExec(evStart(false, "monomind"), evError(monomind.ErrQuota, "usage limit"), evDone(1))
	h := toolHarness(t, (&execScript{turns: []execFunc{quota}}).exec)
	rec := post(h, anyPolicy, h.key(t, "default", "app", false), toolChatBody("claude", weatherTools, weatherQuestion))
	if rec.Code != http.StatusTooManyRequests || h.g.conts.size() != 0 {
		t.Errorf("status %d, %d records: %s", rec.Code, h.g.conts.size(), rec.Body)
	}
}

// A resume that failed for any other reason may have touched the session, and a
// session the runtime could not continue is gone: neither is kept for a retry.
func TestToolsAResumeThatFailedForAnotherReasonIsNotKept(t *testing.T) {
	quota := scriptedExec(evStart(false, "monomind"), evError(monomind.ErrQuota, "usage limit"), evDone(1))
	for name, failures := range map[string][]execFunc{
		"a runner error after the model spoke":                    {scriptedExec(evStart(false, "monomind"), evText("It is"), evError(monomind.ErrRunnerError, "boom"), evDone(1))},
		"a quota after the model spoke":                           {scriptedExec(evStart(false, "monomind"), evText("It is"), evError(monomind.ErrQuota, "usage limit"), evDone(1))},
		"a timeout":                                               {scriptedExec(evStart(false, "monomind"), evError(monomind.ErrTimeout, "too slow"), evDone(1))},
		"a session that is gone, and a replay that hit the quota": {unknownSession(), quota},
	} {
		turns := append([]execFunc{callsWeather(true, "sess-1", "")}, failures...)
		script := &execScript{turns: append(turns, answers("It is 21 C."))}
		h := toolHarness(t, script.exec)
		secret := h.key(t, "default", "app", false)
		id := firstCall(t, h, secret)

		if rec := post(h, anyPolicy, secret, followUp(id, "21 C")); rec.Code == http.StatusOK {
			t.Fatalf("%s: the follow-up should have failed: %s", name, rec.Body)
		}
		rec := post(h, anyPolicy, secret, followUp(id, "21 C"))
		calls := script.calls()
		if last := calls[len(calls)-1]; rec.Code != http.StatusOK || len(calls) != len(turns)+1 || last.Resume != "" {
			t.Errorf("%s: the retry must start from the transcript: status %d, %d turns, resume %q", name, rec.Code, len(calls), last.Resume)
		}
	}
}
