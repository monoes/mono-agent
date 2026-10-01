package openaiapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/monomind"
)

const chatBody = `{"model":"claude/default","messages":[{"role":"user","content":"ping"}]}`

func post(h *harness, p Policy, secret, body string) (rec *httpRecorder) {
	return h.serve(p, http.MethodPost, "/v1/chat/completions", secret, body)
}

func TestChatCompletionHappyPath(t *testing.T) {
	var opts monomind.ExecOptions
	h := newHarness(t, func(ctx context.Context, o monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		opts = o
		return okTurn("pong")(ctx, o, onEvent)
	})
	secret := h.key(t, "default", "app", false)

	rec := post(h, anyPolicy, secret, chatBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
	var got completion
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Object != "chat.completion" || !strings.HasPrefix(got.ID, "chatcmpl-") || got.Model != "claude/default" || got.Created == 0 {
		t.Errorf("envelope: %+v", got)
	}
	if len(got.Choices) != 1 || got.Choices[0].Message.Role != "assistant" || got.Choices[0].Message.Content != "pong" || got.Choices[0].FinishReason != "stop" {
		t.Errorf("choices: %+v", got.Choices)
	}
	if got.Usage == nil || got.Usage.PromptTokens != 11 || got.Usage.CompletionTokens != 7 || got.Usage.TotalTokens != 18 {
		t.Errorf("usage: %+v", got.Usage)
	}
	if rec.Header().Get("X-Request-Id") == "" || rec.Header().Get("X-Monoagent-Model") != "claude/default" {
		t.Errorf("headers: %v", rec.Header())
	}
	if opts.Runtime != "claude" || opts.Model != "" || opts.Prompt != "ping" {
		t.Errorf("Exec options: %+v", opts)
	}
	if rec.Header().Get("X-Monoagent-Context") != "" {
		t.Errorf("a key without context must not report one: %q", rec.Header().Get("X-Monoagent-Context"))
	}
}

func TestChatSandboxStatusHeader(t *testing.T) {
	exec := func(ctx context.Context, o monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		res, err := okTurn("x")(ctx, o, onEvent)
		res.SandboxStatus = monomind.SandboxStatusSandboxed
		return res, err
	}
	h := newHarness(t, exec)
	rec := post(h, anyPolicy, h.key(t, "default", "app", false), `{"model":"codex/gpt-6-astra","messages":[{"role":"user","content":"x"}]}`)
	if rec.Header().Get("X-Monoagent-Sandbox") != "sandboxed" {
		t.Fatalf("X-Monoagent-Sandbox = %q", rec.Header().Get("X-Monoagent-Sandbox"))
	}
}

// A request works in the folder of the profile its key belongs to, never in
// another profile's.
func TestChatRunsInTheKeysProfileFolder(t *testing.T) {
	cwds := map[string]string{}
	var current string
	h := newHarness(t, func(ctx context.Context, o monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		cwds[current] = o.Cwd
		return okTurn("pong")(ctx, o, onEvent)
	})
	for _, profile := range []string{"alice", "bob"} {
		current = profile
		if rec := post(h, anyPolicy, h.key(t, profile, "app", false), chatBody); rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d, body %s", profile, rec.Code, rec.Body)
		}
	}
	for profile, cwd := range cwds {
		// Which slot a request gets does not matter, the profile's folder does.
		if want := filepath.Join(h.scratch, profileFolder(profile)); filepath.Dir(cwd) != want || !strings.HasPrefix(filepath.Base(cwd), "slot-") {
			t.Errorf("profile %s worked in %s, want a slot folder inside %s", profile, cwd, want)
		}
	}
	if len(cwds) != 2 || cwds["alice"] == cwds["bob"] {
		t.Errorf("two profiles must never share a working folder: %v", cwds)
	}
}

// While the server is stopping a request gets a clean 503 it can retry, for a
// stream as well as for a plain completion, and no process is started.
func TestChatIsAnsweredWith503WhileTheServerIsStopping(t *testing.T) {
	var spawned atomic.Int32
	h := newHarness(t, func(ctx context.Context, o monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		spawned.Add(1)
		return okTurn("x")(ctx, o, onEvent)
	})
	secret := h.key(t, "default", "app", false)
	if !h.g.Shutdown(time.Second) {
		t.Fatal("Shutdown must report that nothing is running")
	}
	for name, body := range map[string]string{"plain": chatBody, "stream": streamBody} {
		rec := post(h, anyPolicy, secret, body)
		if rec.Code != http.StatusServiceUnavailable || decodeErrorBody(t, rec)["code"] != "runtime_not_available" {
			t.Errorf("%s: status %d body %s", name, rec.Code, rec.Body)
		}
	}
	if spawned.Load() != 0 {
		t.Errorf("%d turns started while the server was stopping", spawned.Load())
	}
}

func TestChatRejectsBeforeSpawningAnything(t *testing.T) {
	var spawned atomic.Int32
	h := newHarness(t, func(ctx context.Context, o monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		spawned.Add(1)
		return okTurn("x")(ctx, o, onEvent)
	})
	secret := h.key(t, "default", "app", false)

	cases := []struct {
		name   string
		policy Policy
		secret string
		body   string
		status int
		code   string
	}{
		{"no key", anyPolicy, "", chatBody, 401, "invalid_api_key"},
		{"malformed JSON", anyPolicy, secret, `{nope`, 400, "invalid_json"},
		{"no model", anyPolicy, secret, `{"messages":[{"role":"user","content":"x"}]}`, 400, "missing_required_parameter"},
		{"unknown model", anyPolicy, secret, `{"model":"nope/x","messages":[{"role":"user","content":"x"}]}`, 404, "model_not_found"},
		{"auto is not available yet", anyPolicy, secret, `{"model":"auto","messages":[{"role":"user","content":"x"}]}`, 404, "model_not_found"},
		{"flag-shaped model", anyPolicy, secret, `{"model":"claude/--dangerously-skip-permissions","messages":[{"role":"user","content":"x"}]}`, 404, "model_not_found"},
		{"policy denies the runtime", Policy{Max: ChatOnly}, secret, `{"model":"codex/gpt-6-astra","messages":[{"role":"user","content":"x"}]}`, 403, "policy_denied"},
		{"policy denies an unconfined runtime", Policy{Max: Sandboxed}, secret, `{"model":"antigravity","messages":[{"role":"user","content":"x"}]}`, 403, "policy_denied"},
		{"tools", anyPolicy, secret, `{"model":"claude","tools":[{"type":"function","function":{"name":"f"}}],"messages":[{"role":"user","content":"x"}]}`, 400, "unsupported_parameter"},
		{"image input", anyPolicy, secret, `{"model":"claude","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"u"}}]}]}`, 400, "unsupported_parameter"},
		{"no messages", anyPolicy, secret, `{"model":"claude","messages":[]}`, 400, "invalid_value"},
	}
	for _, c := range cases {
		rec := post(h, c.policy, c.secret, c.body)
		if rec.Code != c.status || decodeErrorBody(t, rec)["code"] != c.code {
			t.Errorf("%s: status %d code %v, want %d %s", c.name, rec.Code, decodeErrorBody(t, rec)["code"], c.status, c.code)
		}
	}
	if n := spawned.Load(); n != 0 {
		t.Fatalf("%d turns were spawned for requests that had to be refused first", n)
	}
}

func TestChatRefusesAnOversizedBody(t *testing.T) {
	h := newHarness(t, okTurn("x"), func(_ *Deps, c *Config) { c.BodyLimit = 200 })
	secret := h.key(t, "default", "app", false)
	big := `{"model":"claude","messages":[{"role":"user","content":"` + strings.Repeat("a", 500) + `"}]}`
	rec := post(h, anyPolicy, secret, big)
	if rec.Code != http.StatusRequestEntityTooLarge || decodeErrorBody(t, rec)["code"] != "request_too_large" {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
}

func TestChatMapsRuntimeFailures(t *testing.T) {
	failing := func(code, msg string) execFunc {
		return scriptedExec(evStart(false, "monomind"), evError(code, msg), evDone(1))
	}
	cases := []struct {
		name   string
		exec   execFunc
		status int
		code   string
	}{
		{"quota", failing(monomind.ErrQuota, "usage limit"), 429, "insufficient_quota"},
		{"rate limited", failing(monomind.ErrRateLimited, "slow down"), 429, "rate_limit_exceeded"},
		{"not logged in", failing(monomind.ErrAuth, "run claude login"), 503, "runtime_not_available"},
		{"timeout", failing(monomind.ErrTimeout, "timed out"), 504, "timeout"},
		{"runner error", failing(monomind.ErrRunnerError, "boom"), 502, "runtime_error"},
		{"no done event", scriptedExec(evStart(false, "monomind"), evText("half")), 502, "runtime_error"},
		{"monomind missing", func(context.Context, monomind.ExecOptions, func(monomind.Event)) (*monomind.TurnResult, error) {
			return nil, &monomind.ErrNotFound{}
		}, 503, "runtime_not_available"},
		{"exec failure", func(context.Context, monomind.ExecOptions, func(monomind.Event)) (*monomind.TurnResult, error) {
			return nil, errors.New("disk full")
		}, 500, "internal_error"},
	}
	for _, c := range cases {
		h := newHarness(t, c.exec)
		rec := post(h, anyPolicy, h.key(t, "default", "app", false), chatBody)
		if rec.Code != c.status || decodeErrorBody(t, rec)["code"] != c.code {
			t.Errorf("%s: status %d code %v, want %d %s (body %s)", c.name, rec.Code, decodeErrorBody(t, rec)["code"], c.status, c.code, rec.Body)
		}
	}
}

func TestChatReachingMaxTurnsIsALengthFinish(t *testing.T) {
	h := newHarness(t, scriptedExec(evStart(false, "monomind"), evText("partial"), evResult("partial", monomind.StopMaxTurns), evDone(0)))
	rec := post(h, anyPolicy, h.key(t, "default", "app", false), chatBody)
	var got completion
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if rec.Code != 200 || got.Choices[0].FinishReason != "length" || got.Choices[0].Message.Content != "partial" {
		t.Fatalf("status %d, %+v", rec.Code, got)
	}
}

func TestChatPolicyIsEnforcedAgainstTheStartEventToo(t *testing.T) {
	// The scan said codex is sandboxed, but this turn's start event reports no
	// sandbox at all: the policy (sandboxed at most) must stop it.
	h := newHarness(t, scriptedExec(evStart(false, "none"), evText("x"), evResult("x", monomind.StopEndTurn), evDone(0)))
	rec := post(h, Policy{Max: Sandboxed}, h.key(t, "default", "app", false), `{"model":"codex/gpt-6-astra","messages":[{"role":"user","content":"x"}]}`)
	if rec.Code != http.StatusForbidden || decodeErrorBody(t, rec)["code"] != "policy_denied" {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
}

func TestChatBusyAtTheConcurrencyCap(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	exec := func(ctx context.Context, o monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		close(started)
		<-release
		return okTurn("late")(ctx, o, onEvent)
	}
	h := newHarness(t, exec, func(_ *Deps, c *Config) { c.MaxConcurrent = 1 })
	secret := h.key(t, "default", "app", false)

	done := make(chan *httpRecorder, 1)
	go func() { done <- post(h, anyPolicy, secret, chatBody) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the first turn never reached the runner")
	}

	rec := post(h, anyPolicy, secret, chatBody)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "2" || decodeErrorBody(t, rec)["code"] != "rate_limit_exceeded" {
		t.Fatalf("a second concurrent turn: status %d, headers %v, body %s", rec.Code, rec.Header(), rec.Body)
	}

	close(release)
	if first := <-done; first.Code != 200 {
		t.Fatalf("the first turn: status %d", first.Code)
	}
	// The slot is free again.
	h2exec := okTurn("again")
	h.g.deps.Exec = h2exec
	if third := post(h, anyPolicy, secret, chatBody); third.Code != 200 {
		t.Fatalf("after the first turn ended: status %d, body %s", third.Code, third.Body)
	}
}

func TestChatReasoningEffort(t *testing.T) {
	var effort string
	h := newHarness(t, func(ctx context.Context, o monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		effort = o.Effort
		return okTurn("x")(ctx, o, onEvent)
	})
	secret := h.key(t, "default", "app", false)
	body := func(level string) string {
		return `{"model":"claude/opus[1m]","reasoning_effort":"` + level + `","messages":[{"role":"user","content":"x"}]}`
	}
	post(h, anyPolicy, secret, body("high")) // the model lists low and high
	if effort != "high" {
		t.Errorf("a listed level must be passed on, got %q", effort)
	}
	post(h, anyPolicy, secret, body("minimal")) // not listed: ignored, not an error
	if effort != "" {
		t.Errorf("an unlisted level must be dropped, got %q", effort)
	}
}

func TestChatNeverLogsPromptsOrKeys(t *testing.T) {
	h := newHarness(t, scriptedExec(evStart(false, "monomind"), evError(monomind.ErrRunnerError, "boom"), evDone(1)))
	secret := h.key(t, "default", "app", false)
	post(h, anyPolicy, secret, `{"model":"claude","messages":[{"role":"user","content":"my secret prompt text"}]}`)
	post(h, anyPolicy, "sk-ma-"+strings.Repeat("z", 43), chatBody)

	logs := h.logged()
	if len(logs) == 0 {
		t.Fatal("expected at least one log line for a failed turn")
	}
	for _, line := range logs {
		if strings.Contains(line, "secret prompt") || strings.Contains(line, secret) || strings.Contains(line, strings.Repeat("z", 43)) {
			t.Errorf("a log line carries a prompt or a key: %q", line)
		}
	}
}

func TestChatClientGoneBeforeTheAnswerSendsNothing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	h := newHarness(t, func(c context.Context, o monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		cancel() // the caller hangs up while the turn runs
		<-c.Done()
		return &monomind.TurnResult{Err: &monomind.ProtocolError{Code: monomind.ErrCancelled, Message: "cancelled by caller"}}, nil
	})
	secret := h.key(t, "default", "app", false)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "/v1/chat/completions", strings.NewReader(chatBody))
	req.Header.Set("Authorization", "Bearer "+secret)

	done := make(chan *httpRecorder, 1)
	go func() { done <- h.do(anyPolicy, req) }()
	select {
	case rec := <-done:
		if rec.Body.Len() != 0 {
			t.Errorf("a response was written to a caller that left: %s", rec.Body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the handler did not return after the caller left")
	}
}

func TestChatSandboxedModelsRequireTheirSandboxAndOthersDoNot(t *testing.T) {
	var required []bool
	h := newHarness(t, func(ctx context.Context, o monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		required = append(required, o.RequireSandbox)
		return okTurn("x")(ctx, o, onEvent)
	})
	secret := h.key(t, "default", "app", false)
	for _, model := range []string{"codex/gpt-6-astra", "claude", "antigravity"} {
		post(h, anyPolicy, secret, `{"model":"`+model+`","messages":[{"role":"user","content":"x"}]}`)
	}
	if len(required) != 3 || !required[0] || required[1] || required[2] {
		t.Fatalf("RequireSandbox per turn (sandboxed, chat-only, unconfined) = %v, want [true false false]", required)
	}
}

func TestChatMapsAMissingSandboxToPolicyDenied(t *testing.T) {
	h := newHarness(t, func(context.Context, monomind.ExecOptions, func(monomind.Event)) (*monomind.TurnResult, error) {
		return nil, fmt.Errorf("%w: workspace-write sandbox for codex is unsupported", monomind.ErrSandboxRequired)
	})
	rec := post(h, anyPolicy, h.key(t, "default", "app", false), `{"model":"codex/gpt-6-astra","messages":[{"role":"user","content":"x"}]}`)
	if rec.Code != http.StatusForbidden || decodeErrorBody(t, rec)["code"] != "policy_denied" {
		t.Fatalf("a sandbox that cannot be applied must refuse the turn: %d %s", rec.Code, rec.Body)
	}
}

func TestChatKeepsInternalDetailsOutOfTheResponseAndInTheLog(t *testing.T) {
	const path = "/home/svc/.monoagent/workspaces/api/slot-0"
	logLine := func(h *harness, rec *httpRecorder) string {
		id := rec.Header().Get("X-Request-Id")
		for _, l := range h.logged() {
			if strings.Contains(l, "req="+id) {
				return l
			}
		}
		return ""
	}

	h := newHarness(t, func(context.Context, monomind.ExecOptions, func(monomind.Event)) (*monomind.TurnResult, error) {
		return nil, errors.New("mkdir " + path + ": disk full")
	})
	rec := post(h, anyPolicy, h.key(t, "default", "app", false), chatBody)
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), path) {
		t.Fatalf("a Go error must not reach the client: %d %s", rec.Code, rec.Body)
	}
	if line := logLine(h, rec); !strings.Contains(line, "disk full") || !strings.Contains(line, "status=500") {
		t.Errorf("the log keeps the detail of an internal error: %q", line)
	}

	h2 := newHarness(t, scriptedExec(evStart(false, "monomind"), evError(monomind.ErrRunnerError, "spawn /secret/place ENOENT"), evDone(1)))
	rec2 := post(h2, anyPolicy, h2.key(t, "default", "app", false), chatBody)
	if rec2.Code != http.StatusBadGateway || strings.Contains(rec2.Body.String(), "/secret/place") {
		t.Fatalf("a runtime's own words must not reach the client: %d %s", rec2.Code, rec2.Body)
	}
	if line := logLine(h2, rec2); !strings.Contains(line, monomind.ErrRunnerError) || strings.Contains(line, "/secret/place") {
		t.Errorf("the log keeps the runtime's error code, not its words: %q", line)
	}
}

func TestChatLogLineCarriesTheRequestID(t *testing.T) {
	h := newHarness(t, okTurn("x"))
	rec := post(h, anyPolicy, h.key(t, "default", "app", false), chatBody)
	id := rec.Header().Get("X-Request-Id")
	for _, l := range h.logged() {
		if strings.Contains(l, "req="+id) && strings.Contains(l, "status=200") && strings.Contains(l, "model=claude/default") {
			return
		}
	}
	t.Fatalf("no log line for request %s: %q", id, h.logged())
}

// A deadline of the gateway's own is a timeout, not a caller who left.
func TestChatTheGatewaysOwnDeadlineIsA504(t *testing.T) {
	old := turnGrace
	turnGrace = 30 * time.Millisecond
	t.Cleanup(func() { turnGrace = old })

	h := newHarness(t, func(ctx context.Context, _ monomind.ExecOptions, _ func(monomind.Event)) (*monomind.TurnResult, error) {
		<-ctx.Done() // monomind missed its own --timeout
		return &monomind.TurnResult{SawDone: true, Err: &monomind.ProtocolError{Code: monomind.ErrCancelled, Message: "cancelled"}}, nil
	}, func(_ *Deps, c *Config) { c.TurnTimeout = 20 * time.Millisecond })
	rec := post(h, anyPolicy, h.key(t, "default", "app", false), chatBody)
	if rec.Code != http.StatusGatewayTimeout || decodeErrorBody(t, rec)["code"] != "timeout" {
		t.Fatalf("status %d body %s, want 504 timeout", rec.Code, rec.Body)
	}
}

// The legacy HTTP API server sets a WriteTimeout, and the gateway rides it on
// a loopback bind: a response that takes longer than that must still arrive.
func TestAResponseOutlivesTheServersWriteTimeout(t *testing.T) {
	h := newHarness(t, func(ctx context.Context, o monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		time.Sleep(400 * time.Millisecond)
		return okTurn("slow answer")(ctx, o, onEvent)
	})
	mux := http.NewServeMux()
	h.g.Mount(mux, anyPolicy)
	srv := httptest.NewUnstartedServer(mux)
	srv.Config.WriteTimeout = 150 * time.Millisecond
	srv.Start()
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions", strings.NewReader(chatBody))
	req.Header.Set("Authorization", "Bearer "+h.key(t, "default", "app", false))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("the server's WriteTimeout cut the response: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "slow answer") {
		t.Fatalf("status %d body %s", resp.StatusCode, body)
	}
}
