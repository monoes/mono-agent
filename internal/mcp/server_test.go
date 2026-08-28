package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/storage"
)

// newTestServer builds a Server backed by a fresh temp SQLite DB (real
// migrations applied, same path as the CLI's initDB) and an isolated
// workflow file store, so no real user state is ever touched.
func newTestServer(t *testing.T) *Server {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "mcp-test.db")
	db, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatalf("NewDatabase: %v", err)
	}
	if err := db.ApplyMigrations(); err != nil {
		t.Fatalf("ApplyMigrations: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("closing seed db: %v", err)
	}

	s := NewServer(Options{
		DBPath:       dbPath,
		Profile:      "default",
		WorkflowsDir: filepath.Join(t.TempDir(), "workflows"),
		Version:      "test",
	})
	t.Cleanup(func() { s.rt.Close() })
	return s
}

// serveLines feeds the given JSON-RPC lines to the server and returns the
// parsed response objects, in order.
func serveLines(t *testing.T, s *Server, lines ...string) []map[string]json.RawMessage {
	t.Helper()
	var out bytes.Buffer
	in := strings.NewReader(strings.Join(lines, "\n") + "\n")
	if err := s.Serve(context.Background(), in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	var resps []map[string]json.RawMessage
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("parse response line %q: %v", line, err)
		}
		resps = append(resps, m)
	}
	return resps
}

func request(id int, method string, params interface{}) string {
	b, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	})
	return string(b)
}

func callToolReq(id int, name string, args interface{}) string {
	return request(id, "tools/call", map[string]interface{}{"name": name, "arguments": args})
}

func toolText(t *testing.T, resp map[string]json.RawMessage) (string, bool) {
	t.Helper()
	var tr struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(resp["result"], &tr); err != nil {
		t.Fatalf("unmarshal tool result: %v", err)
	}
	if len(tr.Content) != 1 || tr.Content[0].Type != "text" {
		t.Fatalf("unexpected content shape: %+v", tr.Content)
	}
	return tr.Content[0].Text, tr.IsError
}

func TestServerInitialize(t *testing.T) {
	s := newTestServer(t)
	resps := serveLines(t, s, request(1, "initialize", map[string]interface{}{
		"protocolVersion": "2024-11-05",
	}))
	if len(resps) != 1 {
		t.Fatalf("expected 1 response, got %d", len(resps))
	}
	var res struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
		Capabilities map[string]interface{} `json:"capabilities"`
	}
	if err := json.Unmarshal(resps[0]["result"], &res); err != nil {
		t.Fatalf("unmarshal initialize result: %v", err)
	}
	if res.ProtocolVersion != "2024-11-05" {
		t.Errorf("protocolVersion = %q, want 2024-11-05", res.ProtocolVersion)
	}
	if res.ServerInfo.Name != "monoagentcli" {
		t.Errorf("serverInfo.name = %q", res.ServerInfo.Name)
	}
	if _, ok := res.Capabilities["tools"]; !ok {
		t.Errorf("capabilities.tools missing: %v", res.Capabilities)
	}
	var id json.RawMessage
	if err := json.Unmarshal(resps[0]["id"], &id); err != nil || string(id) != "1" {
		t.Errorf("id not echoed back: %s", resps[0]["id"])
	}
}

func TestServerToolsList(t *testing.T) {
	s := newTestServer(t)
	resps := serveLines(t, s, request(1, "tools/list", nil))
	if len(resps) != 1 {
		t.Fatalf("expected 1 response, got %d", len(resps))
	}
	var res struct {
		Tools []struct {
			Name        string                 `json:"name"`
			InputSchema map[string]interface{} `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(resps[0]["result"], &res); err != nil {
		t.Fatalf("unmarshal tools/list result: %v", err)
	}
	if len(res.Tools) < 10 {
		t.Errorf("expected >= 10 tools, got %d", len(res.Tools))
	}
	want := map[string]bool{
		"workflow_list": false, "workflow_get": false, "workflow_validate": false,
		"workflow_run": false, "workflow_status": false, "node_list": false,
		"node_schema": false, "hil_list": false, "hil_approve": false,
		"hil_reject": false, "docs": false,
	}
	for _, tl := range res.Tools {
		if _, ok := want[tl.Name]; ok {
			want[tl.Name] = true
		}
		if tl.InputSchema == nil {
			t.Errorf("tool %s missing inputSchema", tl.Name)
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("tool %q missing from tools/list", name)
		}
	}
}

func TestServerWorkflowValidateInvalidInline(t *testing.T) {
	s := newTestServer(t)
	// Two nodes in a cycle (a→b→a) — must fail the DAG check; the empty
	// name must fail the save check.
	inline := map[string]interface{}{
		"name": "",
		"nodes": []map[string]interface{}{
			{"id": "a", "type": "core.set", "name": "A", "config": map[string]interface{}{}},
			{"id": "b", "type": "core.set", "name": "B", "config": map[string]interface{}{}},
		},
		"connections": []map[string]interface{}{
			{"source": "a", "source_handle": "main", "target": "b", "target_handle": "main"},
			{"source": "b", "source_handle": "main", "target": "a", "target_handle": "main"},
		},
	}
	resps := serveLines(t, s, callToolReq(1, "workflow_validate", map[string]interface{}{"workflow": inline}))
	if len(resps) != 1 {
		t.Fatalf("expected 1 response, got %d", len(resps))
	}
	text, isErr := toolText(t, resps[0])
	if isErr {
		t.Fatalf("validation of an invalid workflow must be a normal result, got tool error: %s", text)
	}
	var res struct {
		Valid  bool     `json:"valid"`
		Errors []string `json:"errors"`
	}
	if err := json.Unmarshal([]byte(text), &res); err != nil {
		t.Fatalf("unmarshal validate result: %v\n%s", err, text)
	}
	if res.Valid {
		t.Errorf("expected valid=false for cyclic unnamed workflow:\n%s", text)
	}
	if len(res.Errors) == 0 {
		t.Errorf("expected non-empty errors:\n%s", text)
	}
}

func TestServerWorkflowListEmptyDB(t *testing.T) {
	s := newTestServer(t)
	resps := serveLines(t, s, callToolReq(1, "workflow_list", map[string]interface{}{}))
	if len(resps) != 1 {
		t.Fatalf("expected 1 response, got %d", len(resps))
	}
	text, isErr := toolText(t, resps[0])
	if isErr {
		t.Fatalf("workflow_list on empty DB must succeed, got: %s", text)
	}
	if strings.TrimSpace(text) != "[]" {
		t.Errorf("workflow_list on empty DB = %q, want []", text)
	}
}

func TestServerValidateValidInline(t *testing.T) {
	s := newTestServer(t)
	inline := map[string]interface{}{
		"name": "ok",
		"nodes": []map[string]interface{}{
			{"id": "a", "type": "trigger.schedule", "name": "Daily", "config": map[string]interface{}{"cron": "0 0 9 * * *"}},
			{"id": "b", "type": "core.set", "name": "Set", "config": map[string]interface{}{}},
		},
		"connections": []map[string]interface{}{
			{"source": "a", "source_handle": "main", "target": "b", "target_handle": "main"},
		},
	}
	resps := serveLines(t, s, callToolReq(1, "workflow_validate", map[string]interface{}{"workflow": inline}))
	text, isErr := toolText(t, resps[0])
	if isErr {
		t.Fatalf("unexpected tool error: %s", text)
	}
	var res struct {
		Valid  bool     `json:"valid"`
		Errors []string `json:"errors"`
	}
	if err := json.Unmarshal([]byte(text), &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !res.Valid || len(res.Errors) != 0 {
		t.Errorf("expected valid workflow, got: %s", text)
	}
}

func TestServerHilListEmpty(t *testing.T) {
	s := newTestServer(t)
	resps := serveLines(t, s, callToolReq(1, "hil_list", map[string]interface{}{}))
	text, isErr := toolText(t, resps[0])
	if isErr {
		t.Fatalf("hil_list on empty DB must succeed, got: %s", text)
	}
	if strings.TrimSpace(text) != "[]" {
		t.Errorf("hil_list on empty DB = %q, want []", text)
	}
}

func TestServerNodeListAndSchema(t *testing.T) {
	s := newTestServer(t)
	resps := serveLines(t, s, callToolReq(1, "node_list", map[string]interface{}{}))
	text, isErr := toolText(t, resps[0])
	if isErr {
		t.Fatalf("node_list failed: %s", text)
	}
	var nodes []struct {
		Type     string `json:"type"`
		Category string `json:"category"`
		Title    string `json:"title"`
	}
	if err := json.Unmarshal([]byte(text), &nodes); err != nil {
		t.Fatalf("unmarshal node_list: %v", err)
	}
	if len(nodes) < 10 {
		t.Errorf("expected a populated node registry, got %d types", len(nodes))
	}

	resps = serveLines(t, s, callToolReq(2, "node_schema", map[string]interface{}{"type": "no.such_type"}))
	if _, isErr := toolText(t, resps[0]); !isErr {
		t.Errorf("node_schema on unknown type must be a tool error")
	}
}

func TestServerPingUnknownMethodAndNotifications(t *testing.T) {
	s := newTestServer(t)
	notif := `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	resps := serveLines(t, s,
		request(1, "initialize", map[string]interface{}{}),
		notif,
		request(2, "ping", nil),
		request(3, "no/such_method", nil),
	)
	if len(resps) != 3 {
		t.Fatalf("expected 3 responses (notification produces none), got %d", len(resps))
	}
	if string(bytes.TrimSpace(resps[1]["result"])) != "{}" {
		t.Errorf("ping result = %s, want {}", resps[1]["result"])
	}
	var rpcErr struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(resps[2]["error"], &rpcErr); err != nil {
		t.Fatalf("expected error object for unknown method: %v", err)
	}
	if rpcErr.Code != -32601 {
		t.Errorf("unknown method code = %d, want -32601", rpcErr.Code)
	}
}

func TestServerUnknownToolAndMissingID(t *testing.T) {
	s := newTestServer(t)
	resps := serveLines(t, s,
		callToolReq(1, "no_such_tool", map[string]interface{}{}),
		callToolReq(2, "workflow_get", map[string]interface{}{}),
	)
	if len(resps) != 2 {
		t.Fatalf("expected 2 responses, got %d", len(resps))
	}
	if _, isErr := toolText(t, resps[0]); !isErr {
		t.Errorf("unknown tool must return isError:true")
	}
	if _, isErr := toolText(t, resps[1]); !isErr {
		t.Errorf("workflow_get without id must return isError:true")
	}
}

func TestServePipeRoundtrip(t *testing.T) {
	s := newTestServer(t)
	pr, pw := io.Pipe()
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- s.Serve(context.Background(), pr, &out) }()

	go func() {
		fmt.Fprintln(pw, request(1, "initialize", map[string]interface{}{}))
		fmt.Fprintln(pw, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
		fmt.Fprintln(pw, callToolReq(2, "workflow_list", map[string]interface{}{}))
		pw.Close()
	}()

	if err := <-done; err != nil {
		t.Fatalf("Serve: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 response lines over pipe, got %d: %q", len(lines), out.String())
	}
	var last map[string]json.RawMessage
	if err := json.Unmarshal([]byte(lines[1]), &last); err != nil {
		t.Fatalf("parse last line: %v", err)
	}
	text, isErr := toolText(t, last)
	if isErr || strings.TrimSpace(text) != "[]" {
		t.Errorf("workflow_list over pipe = %q (isErr=%v), want []", text, isErr)
	}
}
