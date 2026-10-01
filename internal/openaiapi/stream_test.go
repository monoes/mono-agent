package openaiapi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/monomind"
)

const streamBody = `{"model":"claude","stream":true,"messages":[{"role":"user","content":"hi"}]}`

// sseEvents splits an event stream into its data payloads and counts the
// keep-alive comments.
func sseEvents(body string) (data []string, keepAlives int) {
	for _, block := range strings.Split(body, "\n\n") {
		block = strings.TrimSpace(block)
		switch {
		case strings.HasPrefix(block, "data: "):
			data = append(data, strings.TrimPrefix(block, "data: "))
		case strings.HasPrefix(block, ": keep-alive"):
			keepAlives++
		}
	}
	return data, keepAlives
}

func decodeChunk(t *testing.T, payload string) chunk {
	t.Helper()
	var c chunk
	if err := json.Unmarshal([]byte(payload), &c); err != nil {
		t.Fatalf("%q is not a chunk: %v", payload, err)
	}
	return c
}

// content joins the content deltas of a stream.
func content(t *testing.T, data []string) string {
	t.Helper()
	var b strings.Builder
	for _, d := range data {
		if d == "[DONE]" {
			continue
		}
		for _, ch := range decodeChunk(t, d).Choices {
			if ch.Delta.Content != nil {
				b.WriteString(*ch.Delta.Content)
			}
		}
	}
	return b.String()
}

func TestStreamIncrementalRuntime(t *testing.T) {
	h := newHarness(t, scriptedExec(evStart(true, "monomind"), evText("Hel"), evText("lo"), evUsage(5, 2), evResult("Hello", monomind.StopEndTurn), evDone(0)))
	rec := post(h, anyPolicy, h.key(t, "default", "app", false), streamBody)

	if rec.Code != 200 || rec.Header().Get("Content-Type") != "text/event-stream" || rec.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("status %d headers %v", rec.Code, rec.Header())
	}
	if rec.Header().Get("X-Monoagent-Model") != "claude/default" || rec.Header().Get("X-Request-Id") == "" {
		t.Errorf("headers: %v", rec.Header())
	}
	data, _ := sseEvents(rec.Body.String())
	if len(data) < 5 || data[len(data)-1] != "[DONE]" {
		t.Fatalf("events: %q", data)
	}

	first := decodeChunk(t, data[0])
	if first.Choices[0].Delta.Role != "assistant" || first.Choices[0].Delta.Content == nil || *first.Choices[0].Delta.Content != "" {
		t.Errorf("the first chunk must announce the assistant role: %+v", first)
	}
	if got := content(t, data); got != "Hello" {
		t.Errorf("streamed content = %q, want Hello", got)
	}
	last := decodeChunk(t, data[len(data)-2])
	if last.Choices[0].FinishReason == nil || *last.Choices[0].FinishReason != "stop" {
		t.Errorf("the last chunk must carry finish_reason stop: %+v", last)
	}
	ids := map[string]bool{}
	for _, d := range data[:len(data)-1] {
		c := decodeChunk(t, d)
		ids[c.ID] = true
		if c.Object != "chat.completion.chunk" || c.Model != "claude/default" {
			t.Errorf("chunk envelope: %+v", c)
		}
	}
	if len(ids) != 1 {
		t.Errorf("chunks must share one id, got %v", ids)
	}
	for _, d := range data {
		if strings.Contains(d, `"usage"`) {
			t.Errorf("usage was not asked for but appeared: %s", d)
		}
	}
}

func TestStreamNonIncrementalRuntimeSendsOneChunkAtTheEnd(t *testing.T) {
	h := newHarness(t, scriptedExec(evStart(false, "workspace-write"), evText("Whole answer"), evResult("Whole answer", monomind.StopEndTurn), evDone(0)))
	rec := post(h, anyPolicy, h.key(t, "default", "app", false), `{"model":"codex/gpt-6-astra","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	data, _ := sseEvents(rec.Body.String())
	if len(data) < 3 {
		t.Fatalf("status %d, events: %q", rec.Code, data)
	}

	contentChunks := 0
	for _, d := range data[1 : len(data)-2] { // between the role chunk and the finish chunk
		contentChunks++
		if got := content(t, []string{d}); got != "Whole answer" {
			t.Errorf("content chunk = %q", got)
		}
	}
	if contentChunks != 1 {
		t.Errorf("want exactly one content chunk, got %d in %q", contentChunks, data)
	}
}

func TestStreamIncludeUsageAddsAFinalUsageChunk(t *testing.T) {
	h := newHarness(t, okTurn("x"))
	body := `{"model":"claude","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"hi"}]}`
	rec := post(h, anyPolicy, h.key(t, "default", "app", false), body)
	data, _ := sseEvents(rec.Body.String())
	if len(data) < 3 {
		t.Fatalf("status %d, events: %q", rec.Code, data)
	}
	usageChunk := decodeChunk(t, data[len(data)-2])
	if len(usageChunk.Choices) != 0 || usageChunk.Usage == nil || usageChunk.Usage.PromptTokens != 11 || usageChunk.Usage.TotalTokens != 18 {
		t.Fatalf("usage chunk: %+v", usageChunk)
	}
}

func TestStreamFailureBeforeCommitKeepsItsHTTPStatus(t *testing.T) {
	h := newHarness(t, scriptedExec(evStart(true, "monomind"), evError(monomind.ErrAuth, "run claude login"), evDone(1)))
	rec := post(h, anyPolicy, h.key(t, "default", "app", false), streamBody)
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Content-Type") != "application/json" ||
		decodeErrorBody(t, rec)["code"] != "runtime_not_available" {
		t.Fatalf("status %d type %q body %s", rec.Code, rec.Header().Get("Content-Type"), rec.Body)
	}
}

// openStream posts a streaming request to a real server and returns the
// stream once the 200 is committed, while the turn may still be running, so a
// test reads it as it comes instead of racing a timer.
func openStream(t *testing.T, h *harness, secret, body string) *bufio.Reader {
	t.Helper()
	mux := http.NewServeMux()
	h.g.Mount(mux, anyPolicy)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+secret)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
	return bufio.NewReader(resp.Body)
}

// readLine reads one line, failing the test instead of hanging it.
func readLine(t *testing.T, r *bufio.Reader) string {
	t.Helper()
	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		line, err := r.ReadString('\n')
		ch <- result{line, err}
	}()
	select {
	case v := <-ch:
		if v.err != nil {
			t.Fatalf("the stream ended early: %v", v.err)
		}
		return v.line
	case <-time.After(10 * time.Second):
		t.Fatal("no line from the stream within 10 s")
		return ""
	}
}

func TestStreamFailureAfterCommitIsAnSSEErrorEvent(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	h := newHarness(t, func(ctx context.Context, o monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		<-release
		return scriptedExec(evStart(false, "monomind"), evError(monomind.ErrRunnerError, "it broke"), evDone(1))(ctx, o, onEvent)
	}, func(_ *Deps, c *Config) { c.StreamCommitAfter = 10 * time.Millisecond })
	r := openStream(t, h, h.key(t, "default", "app", false), streamBody) // returns once the 200 is committed
	t.Cleanup(func() { once.Do(func() { close(release) }) })

	once.Do(func() { close(release) })
	rest, _ := io.ReadAll(r)
	data, _ := sseEvents(string(rest))
	if len(data) < 2 || data[len(data)-1] != "[DONE]" {
		t.Fatalf("events: %q", data)
	}
	var e struct {
		Error map[string]any `json:"error"`
	}
	if err := json.Unmarshal([]byte(data[len(data)-2]), &e); err != nil || e.Error["code"] != "runtime_error" {
		t.Fatalf("the error event: %q (%v)", data[len(data)-2], err)
	}
}

func TestStreamSendsKeepAlivesWhileTheTurnIsSilent(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	h := newHarness(t, func(ctx context.Context, o monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		<-release
		return okTurn("done")(ctx, o, onEvent)
	}, func(_ *Deps, c *Config) { c.StreamCommitAfter, c.KeepAlive = 10*time.Millisecond, 10*time.Millisecond })
	r := openStream(t, h, h.key(t, "default", "app", false), streamBody)
	t.Cleanup(func() { once.Do(func() { close(release) }) })

	for keepAlives := 0; keepAlives < 2; {
		if strings.HasPrefix(readLine(t, r), ": keep-alive") {
			keepAlives++
		}
	}
	once.Do(func() { close(release) })
	rest, _ := io.ReadAll(r)
	data, _ := sseEvents(string(rest))
	if got := content(t, data); got != "done" || data[len(data)-1] != "[DONE]" {
		t.Errorf("content = %q, events %q", got, data)
	}
}

func TestStreamGatewayDeadlineAfterCommitIsATimeoutEvent(t *testing.T) {
	old := turnGrace
	turnGrace = 100 * time.Millisecond
	t.Cleanup(func() { turnGrace = old })

	h := newHarness(t, func(ctx context.Context, _ monomind.ExecOptions, _ func(monomind.Event)) (*monomind.TurnResult, error) {
		<-ctx.Done()
		return &monomind.TurnResult{SawDone: true, Err: &monomind.ProtocolError{Code: monomind.ErrCancelled, Message: "cancelled"}}, nil
	}, func(_ *Deps, c *Config) {
		c.TurnTimeout, c.StreamCommitAfter = 500*time.Millisecond, 10*time.Millisecond
	})
	r := openStream(t, h, h.key(t, "default", "app", false), streamBody)
	rest, _ := io.ReadAll(r)
	data, _ := sseEvents(string(rest))
	var e struct {
		Error map[string]any `json:"error"`
	}
	if len(data) < 2 || data[len(data)-1] != "[DONE]" || json.Unmarshal([]byte(data[len(data)-2]), &e) != nil || e.Error["code"] != "timeout" {
		t.Fatalf("a stream ended by the gateway's deadline must end with a timeout event and [DONE]: %q", data)
	}
}

// The server stopping ends the turn, which is not the client leaving: before the
// 200 it is a 502 the client can see, and after it an error event and [DONE],
// never a stream that just stops.
func TestStreamEndedByShutdownBeforeTheCommitIsAnHTTPError(t *testing.T) {
	started := make(chan struct{})
	h := newHarness(t, func(ctx context.Context, _ monomind.ExecOptions, _ func(monomind.Event)) (*monomind.TurnResult, error) {
		close(started)
		<-ctx.Done()
		return &monomind.TurnResult{SawDone: true, Err: &monomind.ProtocolError{Code: monomind.ErrCancelled, Message: "cancelled"}}, nil
	}, func(_ *Deps, c *Config) { c.StreamCommitAfter = time.Minute })
	secret := h.key(t, "default", "app", false)

	got := make(chan *httpRecorder, 1)
	go func() { got <- post(h, anyPolicy, secret, streamBody) }()
	<-started
	h.g.Shutdown(5 * time.Second)

	select {
	case rec := <-got:
		if rec.Code != http.StatusBadGateway || decodeErrorBody(t, rec)["code"] != "runtime_error" {
			t.Fatalf("status %d body %s", rec.Code, rec.Body)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the handler did not return after Shutdown")
	}
}

func TestStreamEndedByShutdownAfterTheCommitIsAnErrorEvent(t *testing.T) {
	h := newHarness(t, func(ctx context.Context, _ monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		onEvent(evStart(true, "monomind"))
		onEvent(evText("first words"))
		<-ctx.Done()
		return &monomind.TurnResult{SawDone: true, Err: &monomind.ProtocolError{Code: monomind.ErrCancelled, Message: "cancelled"}}, nil
	})
	r := openStream(t, h, h.key(t, "default", "app", false), streamBody) // the 200 is committed
	h.g.Shutdown(5 * time.Second)

	rest, _ := io.ReadAll(r)
	data, _ := sseEvents(string(rest))
	var e struct {
		Error map[string]any `json:"error"`
	}
	if len(data) < 2 || data[len(data)-1] != "[DONE]" || json.Unmarshal([]byte(data[len(data)-2]), &e) != nil || e.Error["code"] != "runtime_error" {
		t.Fatalf("a stream cut short by the server stopping must end with an error event and [DONE]: %q", data)
	}
}

// brokenWriter is a client whose connection dies after a few writes.
type brokenWriter struct {
	header    http.Header
	writes    int
	failAfter int
}

func (w *brokenWriter) Header() http.Header { return w.header }
func (w *brokenWriter) WriteHeader(int)     {}
func (w *brokenWriter) Flush()              {}
func (w *brokenWriter) Write(b []byte) (int, error) {
	w.writes++
	if w.writes > w.failAfter {
		return 0, errors.New("broken pipe")
	}
	return len(b), nil
}

// A client that stops reading (the write deadline fires) or whose connection
// breaks must not leave the turn running to its timeout for nobody.
func TestStreamStopsTheTurnWhenAWriteToTheClientFails(t *testing.T) {
	cancelled := make(chan struct{})
	h := newHarness(t, func(ctx context.Context, _ monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		onEvent(evStart(true, "monomind"))
		onEvent(evText("these words cannot be delivered"))
		select {
		case <-ctx.Done():
			close(cancelled)
		case <-time.After(10 * time.Second):
		}
		return &monomind.TurnResult{Err: &monomind.ProtocolError{Code: monomind.ErrCancelled, Message: "cancelled"}}, nil
	})
	w := &brokenWriter{header: http.Header{}, failAfter: 1} // the role chunk goes out, the first delta does not
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	done := make(chan int, 1)
	go func() {
		status, _ := h.g.streamChat(w, req, turn{Runtime: "claude", Model: "default", Prompt: "p", Policy: anyPolicy}, "chatcmpl-x", "claude/default", false)
		done <- status
	}()
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("the turn kept running after a write to the client failed")
	}
	select {
	case status := <-done:
		if status != 499 {
			t.Errorf("status = %d, want 499 (the client is gone)", status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("streamChat did not return")
	}
}

// A denial from the start event comes before any text reaches the client, so
// it is a 403, not a broken stream.
func TestStreamDeniedByTheStartEventIsA403(t *testing.T) {
	h := newHarness(t, scriptedExec(evStart(true, "none"), evText("words from a runtime the policy refuses"), evResult("x", monomind.StopEndTurn), evDone(0)))
	rec := post(h, Policy{Max: Sandboxed}, h.key(t, "default", "app", false),
		`{"model":"codex/gpt-6-astra","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusForbidden || decodeErrorBody(t, rec)["code"] != "policy_denied" || strings.Contains(rec.Body.String(), "words from") {
		t.Fatalf("status %d body %s", rec.Code, rec.Body)
	}
}

func TestStreamClientDisconnectCancelsTheTurn(t *testing.T) {
	cancelled := make(chan struct{})
	exec := func(ctx context.Context, o monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		onEvent(evStart(true, "monomind"))
		onEvent(evText("first words"))
		select {
		case <-ctx.Done():
			close(cancelled)
		case <-time.After(10 * time.Second):
		}
		return &monomind.TurnResult{Err: &monomind.ProtocolError{Code: monomind.ErrCancelled, Message: "cancelled by caller"}}, nil
	}
	h := newHarness(t, exec)
	secret := h.key(t, "default", "app", false)
	mux := http.NewServeMux()
	h.g.Mount(mux, anyPolicy)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/v1/chat/completions", strings.NewReader(streamBody))
	req.Header.Set("Authorization", "Bearer "+secret)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(resp.Body).ReadString('\n') // the role chunk arrived
	if err != nil || !strings.HasPrefix(line, "data: ") {
		t.Fatalf("first line %q (%v)", line, err)
	}
	cancel() // the caller hangs up mid-stream
	resp.Body.Close()

	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("the turn kept running after the client disconnected")
	}
}
