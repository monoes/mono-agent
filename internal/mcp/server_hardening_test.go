package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestServerOversizedLineThenValidRequestStaysAlive: a request line over
// the 17MB guard must be answered with -32700 "request too large" and the
// server must CONTINUE serving the next (valid) request instead of dying.
func TestServerOversizedLineThenValidRequestStaysAlive(t *testing.T) {
	s := newTestServer(t)
	huge := strings.Repeat("a", maxRequestLineBytes+1024)
	var out bytes.Buffer
	in := strings.NewReader(huge + "\n" + request(7, "ping", nil) + "\n")
	if err := s.Serve(context.Background(), in, &out); err != nil {
		t.Fatalf("Serve must survive an oversized line, got error: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 responses (too-large error + ping), got %d: %q", len(lines), out.String())
	}
	var tooLarge map[string]json.RawMessage
	if err := json.Unmarshal([]byte(lines[0]), &tooLarge); err != nil {
		t.Fatalf("parse too-large response: %v", err)
	}
	var rpcErr struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(tooLarge["error"], &rpcErr); err != nil {
		t.Fatalf("too-large response has no error object: %s", lines[0])
	}
	if rpcErr.Code != -32700 || !strings.Contains(rpcErr.Message, "request too large") {
		t.Errorf("oversized line error = %d %q, want -32700 request too large", rpcErr.Code, rpcErr.Message)
	}
	var ping map[string]json.RawMessage
	if err := json.Unmarshal([]byte(lines[1]), &ping); err != nil {
		t.Fatalf("parse ping response: %v", err)
	}
	if string(bytes.TrimSpace(ping["id"])) != "7" {
		t.Errorf("ping after oversized line: id = %s, want 7", ping["id"])
	}
}

// TestServerExactLimitLineSurvives: a request of exactly
// maxRequestLineBytes bytes is within the guard (only over-long lines are
// rejected).
func TestServerExactLimitLineSurvives(t *testing.T) {
	s := newTestServer(t)
	prefix := `{"jsonrpc":"2.0","id":1,"method":"ping","pad":"`
	suffix := `"}`
	pad := maxRequestLineBytes - len(prefix) - len(suffix)
	line := prefix + strings.Repeat("a", pad) + suffix
	if len(line) != maxRequestLineBytes {
		t.Fatalf("test construction: line is %d bytes, want %d", len(line), maxRequestLineBytes)
	}
	var out bytes.Buffer
	in := strings.NewReader(line + "\n")
	if err := s.Serve(context.Background(), in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if !strings.Contains(out.String(), `"id":1`) {
		t.Errorf("in-limit line was rejected: %s", out.String())
	}
}

// TestServerIDlessPingNoResponse: a request with NO raw "id" member is a
// notification regardless of method — no response line may be emitted
// (previously the server answered with id:null).
func TestServerIDlessPingNoResponse(t *testing.T) {
	s := newTestServer(t)
	resps := serveLines(t, s,
		`{"jsonrpc":"2.0","method":"ping"}`,
		`{"jsonrpc":"2.0","method":"tools/list"}`,
		request(3, "ping", nil),
	)
	if len(resps) != 1 {
		t.Fatalf("expected exactly 1 response (only the id-ful ping), got %d", len(resps))
	}
	if string(bytes.TrimSpace(resps[0]["id"])) != "3" {
		t.Errorf("response id = %s, want 3", resps[0]["id"])
	}
}

// TestServerNullIDTreatedAsNotification: an explicit "id": null member is
// treated the same as an absent id.
func TestServerNullIDTreatedAsNotification(t *testing.T) {
	s := newTestServer(t)
	resps := serveLines(t, s, `{"jsonrpc":"2.0","id":null,"method":"ping"}`)
	if len(resps) != 0 {
		t.Fatalf("expected no response for id:null ping, got %d", len(resps))
	}
}

// TestServerNotificationWithIDIsError: a notifications/* method arriving
// with a non-null id is a protocol violation → JSON-RPC error.
func TestServerNotificationWithIDIsError(t *testing.T) {
	s := newTestServer(t)
	resps := serveLines(t, s, `{"jsonrpc":"2.0","id":42,"method":"notifications/initialized"}`)
	if len(resps) != 1 {
		t.Fatalf("expected 1 error response, got %d", len(resps))
	}
	var rpcErr struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(resps[0]["error"], &rpcErr); err != nil {
		t.Fatalf("expected error object: %v", err)
	}
	if rpcErr.Code != -32600 {
		t.Errorf("notification-with-id code = %d, want -32600", rpcErr.Code)
	}
}

// TestServerMalformedLineParseError: unparseable JSON → -32700 with id null.
func TestServerMalformedLineParseError(t *testing.T) {
	s := newTestServer(t)
	resps := serveLines(t, s, `{this is not json`)
	if len(resps) != 1 {
		t.Fatalf("expected 1 response, got %d", len(resps))
	}
	var rpcErr struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(resps[0]["error"], &rpcErr); err != nil {
		t.Fatalf("expected error object: %v", err)
	}
	if rpcErr.Code != -32700 {
		t.Errorf("malformed line code = %d, want -32700", rpcErr.Code)
	}
	if string(bytes.TrimSpace(resps[0]["id"])) != "null" {
		t.Errorf("malformed line id = %s, want null", resps[0]["id"])
	}
}

// TestWorkflowValidateFileErrorsCollapsed: workflow_validate's file param
// must keep the distinct not-found message but collapse every other
// read/parse failure to a fixed "file is not a valid workflow" without
// content-derived detail.
func TestWorkflowValidateFileErrorsCollapsed(t *testing.T) {
	s := newTestServer(t)
	dir := t.TempDir()

	resps := serveLines(t, s, callToolReq(1, "workflow_validate",
		map[string]interface{}{"file": filepath.Join(dir, "missing.json")}))
	text, isErr := toolText(t, resps[0])
	if !isErr {
		t.Fatalf("missing file must be a tool error, got: %s", text)
	}
	if !strings.Contains(text, "not found") {
		t.Errorf("missing file must keep a distinct not-found message, got: %s", text)
	}

	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`{"name": `), 0o644); err != nil {
		t.Fatalf("write bad.json: %v", err)
	}
	resps = serveLines(t, s, callToolReq(2, "workflow_validate", map[string]interface{}{"file": bad}))
	text, isErr = toolText(t, resps[0])
	if !isErr || !strings.Contains(text, "file is not a valid workflow") {
		t.Fatalf("invalid file must collapse to the fixed message, got (isErr=%v): %s", isErr, text)
	}
	if strings.Contains(text, "unexpected EOF") || strings.Contains(text, "invalid character") {
		t.Errorf("parse detail leaked into error: %s", text)
	}
}

// TestServerServeReturnsOnEOF: when the client closes stdin the read loop
// ends and Serve returns (which also cancels the serve context handed to
// handlers).
func TestServerServeReturnsOnEOF(t *testing.T) {
	s := newTestServer(t)
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- s.Serve(context.Background(), pr, &bytes.Buffer{}) }()
	_ = pw.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve after EOF: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after stdin EOF")
	}
}

// TestWaitForExecutionObservesContextCancel pins the wiring the serve
// context relies on: a cancelled context (e.g. stdin EOF) must cut the
// workflow_run wait short instead of blocking out the full timeout.
func TestWaitForExecutionObservesContextCancel(t *testing.T) {
	s := newTestServer(t)
	rt, err := s.runtime()
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	exec := waitForExecution(ctx, rt, "no-such-execution", 10*time.Minute)
	if exec == nil || exec.Status != "UNKNOWN" {
		t.Fatalf("cancelled wait = %+v, want UNKNOWN snapshot", exec)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("waitForExecution ignored ctx cancellation (took %v)", elapsed)
	}
}

func TestClampTimeoutSeconds(t *testing.T) {
	cases := []struct {
		name string
		in   float64
		want time.Duration
	}{
		{"zero → default", 0, 120 * time.Second},
		{"negative → default", -5, 120 * time.Second},
		{"sub-second clamps up to 1s", 0.25, 1 * time.Second},
		{"one second", 1, 1 * time.Second},
		{"mid-range passes", 300.5, 300500 * time.Millisecond},
		{"upper bound", 600, 600 * time.Second},
		{"clamps down to 600s", 100000, 600 * time.Second},
	}
	for _, tc := range cases {
		if got := clampTimeoutSeconds(tc.in); got != tc.want {
			t.Errorf("%s: clampTimeoutSeconds(%v) = %v, want %v", tc.name, tc.in, got, tc.want)
		}
	}
}

// TestReadLineGuard unit-tests the line reader: exact-limit lines pass,
// over-limit lines are flagged and fully consumed (the next read starts at
// a fresh line), EOF surfaces as io.EOF with the trailing content.
func TestReadLineGuard(t *testing.T) {
	r := bufio.NewReader(strings.NewReader(strings.Repeat("a", 9) + "\n" + strings.Repeat("b", 11) + "\n" + "tail"))
	line, tooLong, err := readLine(r, 10)
	if err != nil || tooLong || strings.TrimRight(string(line), "\n") != strings.Repeat("a", 9) {
		t.Fatalf("in-limit line: (%q, %v, %v)", line, tooLong, err)
	}
	line, tooLong, err = readLine(r, 10)
	if err != nil || !tooLong || len(line) != 0 {
		t.Fatalf("over-limit line: (%q, %v, %v) — want discarded + tooLong", line, tooLong, err)
	}
	line, tooLong, err = readLine(r, 10)
	if err == nil || tooLong || string(line) != "tail" {
		t.Fatalf("final unterminated line: (%q, %v, %v) — want EOF with content", line, tooLong, err)
	}
}
