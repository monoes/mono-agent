package httpnodes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/workflow"
)

func runRequest(t *testing.T, config map[string]interface{}) []workflow.NodeOutput {
	t.Helper()
	n := &RequestNode{}
	out, err := n.Execute(context.Background(), workflow.NodeInput{}, config)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	return out
}

func handleItems(out []workflow.NodeOutput, handle string) []workflow.Item {
	for _, o := range out {
		if o.Handle == handle {
			return o.Items
		}
	}
	return nil
}

func TestRequestNode_GetSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.Header.Get("X-Test") != "hello" {
			t.Errorf("custom header not forwarded: %q", r.Header.Get("X-Test"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer srv.Close()

	out := runRequest(t, map[string]interface{}{
		"method":          "GET",
		"url":             srv.URL,
		"response_format": "json",
		"headers":         map[string]interface{}{"X-Test": "hello"},
	})
	main := handleItems(out, "main")
	if len(main) != 1 {
		t.Fatalf("expected 1 main item, got %d", len(main))
	}
	switch s := main[0].JSON["status"].(type) {
	case int:
		if s != 200 {
			t.Errorf("status = %d, want 200", s)
		}
	case float64:
		if int(s) != 200 {
			t.Errorf("status = %v, want 200", s)
		}
	default:
		t.Errorf("unexpected status type %T (%v)", s, s)
	}
}

func TestRequestNode_ErrorHandleOnBadHost(t *testing.T) {
	// An unroutable URL must route to the "error" handle, not crash or hang.
	out := runRequest(t, map[string]interface{}{
		"method":          "GET",
		"url":             "http://127.0.0.1:1/never",
		"timeout_seconds": 2.0,
	})
	if errs := handleItems(out, "error"); len(errs) == 0 {
		t.Fatalf("expected an error item for an unreachable host, got %v", out)
	}
	if main := handleItems(out, "main"); len(main) != 0 {
		t.Errorf("expected no main items on failure, got %d", len(main))
	}
}

func TestRequestNode_RequiresURL(t *testing.T) {
	n := &RequestNode{}
	_, err := n.Execute(context.Background(), workflow.NodeInput{}, map[string]interface{}{"method": "GET"})
	if err == nil {
		t.Error("expected an error when url is missing")
	}
}

// bodyServer serves n NUL bytes with an explicit Content-Length so the
// over-cap error can report it (streams 1MB chunks — no big allocation
// server-side).
func bodyServer(t *testing.T, n int64) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.FormatInt(n, 10))
		w.WriteHeader(200)
		chunk := make([]byte, 1<<20)
		remaining := n
		for remaining > 0 {
			next := int64(len(chunk))
			if remaining < next {
				next = remaining
			}
			if _, err := w.Write(chunk[:next]); err != nil {
				return
			}
			remaining -= next
		}
	}))
}

func TestRequestNode_BodyUnderCapFlows(t *testing.T) {
	// 10MB body flows through the default 64MB cap untouched.
	srv := bodyServer(t, 10<<20)
	defer srv.Close()

	out := runRequest(t, map[string]interface{}{
		"method":          "GET",
		"url":             srv.URL,
		"response_format": "text",
	})
	main := handleItems(out, "main")
	if len(main) != 1 {
		t.Fatalf("expected 1 main item, got %d", len(main))
	}
	body, _ := main[0].JSON["body"].(string)
	if len(body) != 10<<20 {
		t.Errorf("body length = %d, want %d", len(body), 10<<20)
	}
}

func TestRequestNode_BodyOverDefaultCapErrors(t *testing.T) {
	// 70MB body exceeds the 64MB default cap: clean error item naming the
	// cap and the real content-length, no memory blowup, no crash.
	srv := bodyServer(t, 70<<20)
	defer srv.Close()

	out := runRequest(t, map[string]interface{}{
		"method":          "GET",
		"url":             srv.URL,
		"response_format": "text",
	})
	errs := handleItems(out, "error")
	if len(errs) != 1 {
		t.Fatalf("expected 1 error item, got %d (%v)", len(errs), out)
	}
	msg, _ := errs[0].JSON["error"].(string)
	if !strings.Contains(msg, "exceeds max_body_mb (64)") {
		t.Errorf("error must name the default cap: %q", msg)
	}
	if !strings.Contains(msg, "content-length: 73400320") {
		t.Errorf("error must include actual content-length: %q", msg)
	}
	if !strings.Contains(msg, "use max_body_mb to raise") {
		t.Errorf("error must point at the knob: %q", msg)
	}
	if main := handleItems(out, "main"); len(main) != 0 {
		t.Errorf("expected no main items, got %d", len(main))
	}
}

func TestRequestNode_BodyCustomCap(t *testing.T) {
	// Raising max_body_mb lets the same 70MB body through.
	srv := bodyServer(t, 70<<20)
	defer srv.Close()

	out := runRequest(t, map[string]interface{}{
		"method":          "GET",
		"url":             srv.URL,
		"response_format": "text",
		"max_body_mb":     100.0,
	})
	main := handleItems(out, "main")
	if len(main) != 1 {
		t.Fatalf("expected main item with raised cap, got %v", out)
	}
	if body, _ := main[0].JSON["body"].(string); len(body) != 70<<20 {
		t.Errorf("body length = %d, want %d", len(body), 70<<20)
	}
}

func TestSSHNode_ValidatesConfig(t *testing.T) {
	n := &SSHNode{}
	// Missing host, username, command all error before any network use.
	for _, cfg := range []map[string]interface{}{
		{},
		{"host": "h"},
		{"host": "h", "username": "u"},
	} {
		if _, err := n.Execute(context.Background(), workflow.NodeInput{}, cfg); err == nil {
			t.Errorf("SSHNode.Execute(%v) = nil error, want validation error", cfg)
		}
	}
}

func TestFTPNode_ValidatesConfig(t *testing.T) {
	n := &FTPNode{}
	for _, cfg := range []map[string]interface{}{
		{},
		{"operation": "download"},
		{"operation": "download", "host": "h"},
	} {
		if _, err := n.Execute(context.Background(), workflow.NodeInput{}, cfg); err == nil {
			t.Errorf("FTPNode.Execute(%v) = nil error, want validation error", cfg)
		}
	}
}
