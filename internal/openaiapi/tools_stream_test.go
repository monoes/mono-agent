package openaiapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/monomind"
)

// rawChunk is a chunk with its delta kept as written, so a test sees exactly
// which keys are there.
type rawChunk struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Choices []struct {
		Delta        map[string]json.RawMessage `json:"delta"`
		FinishReason *string                    `json:"finish_reason"`
	} `json:"choices"`
	Usage *usage `json:"usage"`
}

func decodeRawChunks(t *testing.T, data []string) []rawChunk {
	t.Helper()
	var out []rawChunk
	for _, d := range data {
		if d == "[DONE]" {
			continue
		}
		var c rawChunk
		if err := json.Unmarshal([]byte(d), &c); err != nil {
			t.Fatalf("%q is not a chunk: %v", d, err)
		}
		out = append(out, c)
	}
	return out
}

func streamToolBody(extra string) string {
	return toolChatBody("claude", weatherTools+`,"stream":true`+extra, weatherQuestion)
}

func TestToolsStreamACallArrivesAsToolCallChunks(t *testing.T) {
	h := toolHarness(t, fakeLegExec(func(ctx context.Context, emit func(monomind.Event)) {
		emit(evStart(true, "monomind"))
		emit(evSession("sess-1"))
		emit(evText("Looking "))
		emit(evText("it up."))
		emit(evCall("get_weather", `{"city":"Paris"}`))
		emit(evText("never sent")) // a runtime that goes on talking while it waits
		<-ctx.Done()
	}))
	rec := post(h, anyPolicy, h.key(t, "default", "app", false), streamToolBody(`,"stream_options":{"include_usage":true}`))
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("status %d type %q body %s", rec.Code, rec.Header().Get("Content-Type"), rec.Body)
	}
	data, _ := sseEvents(rec.Body.String())
	if len(data) != 7 || data[len(data)-1] != "[DONE]" {
		t.Fatalf("events: %q", data)
	}
	chunks := decodeRawChunks(t, data)
	if string(chunks[0].Choices[0].Delta["role"]) != `"assistant"` {
		t.Errorf("the first chunk announces the role: %v", chunks[0].Choices[0].Delta)
	}
	if got := content(t, data); got != "Looking it up." {
		t.Errorf("content = %q: the words before the call stream, the ones after it do not", got)
	}

	// The call: its id, type and name with empty arguments, then the arguments.
	a, b := chunks[3].Choices[0].Delta["tool_calls"], chunks[4].Choices[0].Delta["tool_calls"]
	var head, tail []struct {
		Index    *int   `json:"index"`
		ID       string `json:"id"`
		Type     string `json:"type"`
		Function struct {
			Name      string  `json:"name"`
			Arguments *string `json:"arguments"`
		} `json:"function"`
	}
	if err := json.Unmarshal(a, &head); err != nil || len(head) != 1 {
		t.Fatalf("first call chunk: %s (%v)", a, err)
	}
	if err := json.Unmarshal(b, &tail); err != nil || len(tail) != 1 {
		t.Fatalf("second call chunk: %s (%v)", b, err)
	}
	if head[0].Index == nil || *head[0].Index != 0 || !strings.HasPrefix(head[0].ID, "call_") || head[0].Type != "function" ||
		head[0].Function.Name != "get_weather" || head[0].Function.Arguments == nil || *head[0].Function.Arguments != "" {
		t.Errorf("the chunk that opens the call: %s", a)
	}
	if tail[0].Index == nil || *tail[0].Index != 0 || tail[0].ID != "" || tail[0].Type != "" || tail[0].Function.Name != "" ||
		tail[0].Function.Arguments == nil || *tail[0].Function.Arguments != `{"city":"Paris"}` {
		t.Errorf("the chunk that carries the arguments: %s", b)
	}
	last := chunks[5].Choices[0]
	if last.FinishReason == nil || *last.FinishReason != "tool_calls" || len(last.Delta) != 0 {
		t.Errorf("the last chunk finishes with tool_calls: %+v", last)
	}
	for _, d := range data {
		if strings.Contains(d, `"usage"`) {
			t.Errorf("a leg that ended at a call promises no usage, even when asked: %s", d)
		}
	}
	ids := map[string]bool{}
	for _, c := range chunks {
		ids[c.ID] = true
		if c.Object != "chat.completion.chunk" {
			t.Errorf("object %q", c.Object)
		}
	}
	if len(ids) != 1 {
		t.Errorf("one id for the whole stream: %v", ids)
	}
}

func TestToolsStreamNarrationOfARuntimeThatDoesNotStreamIsOneChunkBeforeTheCall(t *testing.T) {
	h := toolHarness(t, fakeLegExec(func(ctx context.Context, emit func(monomind.Event)) {
		emit(evStart(false, "workspace-write"))
		emit(evSession("thread-1"))
		emit(evText("First thought."))
		emit(evText("Second thought."))
		emit(evCall("get_weather", `{"city":"Paris"}`))
		<-ctx.Done()
	}))
	rec := post(h, anyPolicy, h.key(t, "default", "app", false), strings.Replace(streamToolBody(""), `"model":"claude"`, `"model":"codex/gpt-6-astra"`, 1))
	data, _ := sseEvents(rec.Body.String())
	chunks := decodeRawChunks(t, data)
	if len(chunks) != 5 { // role, the narration, the call's two chunks, finish
		t.Fatalf("events: %q", data)
	}
	if got := content(t, data); got != "First thought.\n\nSecond thought." {
		t.Errorf("content = %q", got)
	}
	if _, isContent := chunks[1].Choices[0].Delta["content"]; !isContent {
		t.Errorf("the narration is the chunk after the role chunk: %v", chunks[1].Choices[0].Delta)
	}
	if _, isCall := chunks[2].Choices[0].Delta["tool_calls"]; !isCall {
		t.Errorf("the call follows the narration: %v", chunks[2].Choices[0].Delta)
	}
}

func TestToolsStreamACallWithoutWords(t *testing.T) {
	h := toolHarness(t, callsWeather(true, "s", ""))
	rec := post(h, anyPolicy, h.key(t, "default", "app", false), streamToolBody(""))
	data, _ := sseEvents(rec.Body.String())
	chunks := decodeRawChunks(t, data)
	if len(chunks) != 4 { // role, the call's two chunks, finish
		t.Fatalf("events: %q", data)
	}
	if got := content(t, data); got != "" {
		t.Errorf("content = %q", got)
	}
}

func TestToolsStreamAnAnswerIsAnOrdinaryStream(t *testing.T) {
	h := toolHarness(t, answers("It is 21 C."))
	rec := post(h, anyPolicy, h.key(t, "default", "app", false), streamToolBody(`,"stream_options":{"include_usage":true}`))
	data, _ := sseEvents(rec.Body.String())
	if got := content(t, data); got != "It is 21 C." || data[len(data)-1] != "[DONE]" {
		t.Fatalf("content %q events %q", got, data)
	}
	finish := decodeChunk(t, data[len(data)-3])
	usageChunk := decodeChunk(t, data[len(data)-2])
	if finish.Choices[0].FinishReason == nil || *finish.Choices[0].FinishReason != "stop" || usageChunk.Usage == nil || usageChunk.Usage.TotalTokens != 11 {
		t.Errorf("finish %+v usage %+v", finish, usageChunk)
	}
}

func TestToolsStreamAFollowUpResumesTheSession(t *testing.T) {
	script := &execScript{turns: []execFunc{callsWeather(true, "sess-1", ""), answers("It is 21 C.")}}
	h := toolHarness(t, script.exec)
	secret := h.key(t, "default", "app", false)
	rec := post(h, anyPolicy, secret, streamToolBody(""))
	data, _ := sseEvents(rec.Body.String())
	var callID string
	for _, c := range decodeRawChunks(t, data) {
		if raw, ok := c.Choices[0].Delta["tool_calls"]; ok {
			var calls []struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(raw, &calls)
			if calls[0].ID != "" {
				callID = calls[0].ID
			}
		}
	}
	if callID == "" {
		t.Fatalf("no call id in %q", data)
	}
	rec = post(h, anyPolicy, secret, strings.Replace(followUp(callID, "21 C"), `"messages"`, `"stream":true,"messages"`, 1))
	data, _ = sseEvents(rec.Body.String())
	if got := content(t, data); got != "It is 21 C." {
		t.Fatalf("content %q: %q", got, data)
	}
	if got := script.calls()[1]; got.Resume != "sess-1" {
		t.Errorf("the streamed follow-up resumed %q", got.Resume)
	}
}

func TestToolsStreamAFailureBeforeTheCommitKeepsItsHTTPStatus(t *testing.T) {
	h := toolHarness(t, scriptedExec(evStart(true, "monomind"), evError(monomind.ErrAuth, "run claude login"), evDone(1)))
	rec := post(h, anyPolicy, h.key(t, "default", "app", false), streamToolBody(""))
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Content-Type") != "application/json" || decodeErrorBody(t, rec)["code"] != "runtime_not_available" {
		t.Fatalf("status %d type %q body %s", rec.Code, rec.Header().Get("Content-Type"), rec.Body)
	}
	// The refusal of a runtime that does not serve tools is a plain 400 as well.
	rec = post(h, anyPolicy, h.key(t, "default", "app2", false), strings.Replace(streamToolBody(""), `"model":"claude"`, `"model":"antigravity"`, 1))
	if rec.Code != http.StatusBadRequest || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("status %d type %q body %s", rec.Code, rec.Header().Get("Content-Type"), rec.Body)
	}
}

func TestToolsStreamAFailureAfterTheCommitIsAnSSEErrorEvent(t *testing.T) {
	h := toolHarness(t, scriptedExec(evStart(true, "monomind"), evSession("s"), evText("first words"), evError(monomind.ErrRunnerError, "it broke"), evDone(1)))
	rec := post(h, anyPolicy, h.key(t, "default", "app", false), streamToolBody(""))
	data, _ := sseEvents(rec.Body.String())
	var e struct {
		Error map[string]any `json:"error"`
	}
	if len(data) < 3 || data[len(data)-1] != "[DONE]" || json.Unmarshal([]byte(data[len(data)-2]), &e) != nil || e.Error["code"] != "runtime_error" {
		t.Fatalf("the stream must end with an error event and [DONE]: %q", data)
	}
	if content(t, data[:len(data)-2]) != "first words" {
		t.Errorf("what was said before the failure stays: %q", data)
	}
}

// A client that stops reading must not leave the leg running for nobody.
func TestToolsStreamAWriteThatFailsEndsTheLeg(t *testing.T) {
	cancelled := make(chan struct{})
	h := toolHarness(t, func(ctx context.Context, _ monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		onEvent(evStart(true, "monomind"))
		onEvent(evText("these words cannot be delivered"))
		select {
		case <-ctx.Done():
			close(cancelled)
		case <-time.After(10 * time.Second):
		}
		return &monomind.TurnResult{Err: &monomind.ProtocolError{Code: monomind.ErrCancelled, Message: "cancelled"}}, nil
	})
	req := toolRequest(t, weatherTools+`,"stream":true`, weatherQuestion)
	m := ModelInfo{ID: "claude/default", Runtime: "claude", Model: "default", Class: ChatOnly}
	tn := turn{Runtime: "claude", Model: "default", Prompt: "hi", Policy: anyPolicy, ProfileID: "default"}
	w := &brokenWriter{header: http.Header{}, failAfter: 1} // the role chunk goes out, the first delta does not

	done := make(chan int, 1)
	go func() {
		status, _, _ := h.g.toolChat(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil), Principal{KeyID: "k", ProfileID: "default"}, req, tn, m, anyPolicy, "chatcmpl-x")
		done <- status
	}()
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("the leg kept running after a write to the client failed")
	}
	select {
	case status := <-done:
		if status != 499 {
			t.Errorf("status = %d, want 499 (the client is gone)", status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("toolChat did not return")
	}
}

func TestToolsStreamKeepAlivesFlowWhileTheRuntimeThinks(t *testing.T) {
	release := make(chan struct{})
	h := toolHarness(t, func(ctx context.Context, o monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		<-release
		return callsWeather(true, "s", "")(ctx, o, onEvent)
	}, func(_ *Deps, c *Config) { c.StreamCommitAfter, c.KeepAlive = 10*time.Millisecond, 10*time.Millisecond })
	r := openStream(t, h, h.key(t, "default", "app", false), streamToolBody(""))
	for keepAlives := 0; keepAlives < 2; {
		if strings.HasPrefix(readLine(t, r), ": keep-alive") {
			keepAlives++
		}
	}
	close(release)
	rest, _ := io.ReadAll(r)
	data, _ := sseEvents(string(rest))
	if len(data) < 3 || data[len(data)-1] != "[DONE]" || !strings.Contains(string(rest), `"finish_reason":"tool_calls"`) {
		t.Errorf("the stream after the keep-alives: %q", data)
	}
}
