package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestMessageListRendersEvenThoughNotJSON pins the adapter's json.Valid
// guard: chat's list_messages/get_message return marshalFenced(...) output
// (a plain-text untrusted-content fence, not a JSON document as a whole).
// Promoting that unconditionally to json.RawMessage breaks downstream
// json.MarshalIndent in callTool, surfacing as a generic "render tool
// result" error — i.e. these two tools would be dead on arrival every call.
func TestMessageListRendersEvenThoughNotJSON(t *testing.T) {
	s := newTestServer(t)
	resps := serveLines(t, s, callToolReq(1, "message_list", map[string]interface{}{}))
	text, isErr := toolText(t, resps[0])
	if isErr && strings.Contains(text, "render tool result") {
		t.Fatalf("message_list failed to render its own (non-JSON, fenced) output: %s", text)
	}
	if isErr {
		t.Fatalf("message_list on an empty DB must succeed, got tool error: %s", text)
	}
}

// TestMonoagentAdaptedToolsNeverFailToRender is the regression guard for the
// json.Valid bug class pinned by TestMessageListRendersEvenThoughNotJSON:
// call every chat-adapted tool with {} args and assert none of them ever
// produce the specific "render tool result" error — domain-validation
// errors ("id is required" etc.) are expected and fine, since args are
// empty; a render failure is not, since it would mean the adapter itself is
// broken for that tool regardless of what arguments it's called with.
func TestMonoagentAdaptedToolsNeverFailToRender(t *testing.T) {
	s := newTestServerAllowMutations(t, true)
	var lines []string
	for i, tl := range monoagentAdaptedTools() {
		lines = append(lines, callToolReq(i+1, tl.name, map[string]interface{}{}))
	}
	resps := serveLines(t, s, lines...)
	if len(resps) != len(monoagentAdaptedTools()) {
		t.Fatalf("expected %d responses, got %d", len(monoagentAdaptedTools()), len(resps))
	}
	for i, tl := range monoagentAdaptedTools() {
		resp := respByID(t, resps, jsonInt(i+1))
		text, isErr := toolText(t, resp)
		if isErr && strings.Contains(text, "render tool result") {
			t.Errorf("tool %q failed to render: %s", tl.name, text)
		}
	}
}

func jsonInt(i int) string {
	b, _ := json.Marshal(i)
	return string(b)
}

// TestServerMutatingToolSucceedsWithFlag: with the gate open, mutating
// chat-adapted tools actually run. workflow_create and workflow_delete are
// exercised in one Serve() call (Server tears its runtime's DB connection
// down at the end of every Serve — fine in production, where Serve is only
// ever called once per process, but it means dependent DB state must live
// within a single serveLines batch in tests): workflow_create proves the
// adapter can create real rows, and workflow_delete proves it on a
// separately pre-seeded workflow (avoids needing to correlate create's
// server-generated id into delete's request within the same wire batch).
func TestServerMutatingToolSucceedsWithFlag(t *testing.T) {
	s := newTestServerAllowMutations(t, true)
	rt, err := s.runtime()
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	// Seeded directly against the workflows table, not via rt.store: chat's
	// deleteWorkflow (internal/ai/chat/monoagent_tools.go) queries that SQL
	// table directly rather than through the hybrid file+SQL store, so a
	// row created through the store wouldn't necessarily be visible to it.
	// profile_id is left NULL (not in the base schema, added by a later
	// migration) — deleteWorkflow's own ownership check treats a NULL as
	// "default" via COALESCE(profile_id,'default'), matching this server's
	// profile.
	const seedID = "wf-mcp-adapter-delete-test"
	if _, err := rt.db.DB.Exec(`INSERT INTO workflows (id, name) VALUES (?, ?)`, seedID, "seed-for-delete"); err != nil {
		t.Fatalf("seed workflow: %v", err)
	}

	resps := serveLines(t, s,
		callToolReq(1, "workflow_create", map[string]interface{}{"name": "mcp-adapter-create-test"}),
		callToolReq(2, "workflow_delete", map[string]interface{}{"workflow_id": seedID}),
	)

	createText, isErr := toolText(t, respByID(t, resps, "1"))
	if isErr {
		t.Fatalf("workflow_create failed: %s", createText)
	}
	if !strings.Contains(createText, "workflow_id") {
		t.Errorf("workflow_create result = %s, want a workflow_id field", createText)
	}

	deleteText, isErr := toolText(t, respByID(t, resps, "2"))
	if isErr {
		t.Fatalf("workflow_delete failed: %s", deleteText)
	}
	if !strings.Contains(deleteText, "backup") {
		t.Errorf("workflow_delete result = %s, want it to mention the pre-delete backup", deleteText)
	}
}

// TestServerSecretValuesNeverReturnedViaMCP: a secret value added through
// the MCP path must never come back through it either — not from the add
// call's own response, and not from a subsequent secret_list. Scoped
// precisely to vault secret values; it does not (and is not meant to)
// cover workflow_get's separate, pre-existing lack of inline node-config
// redaction — see the plan's Dedup section.
func TestServerSecretValuesNeverReturnedViaMCP(t *testing.T) {
	s := newTestServerAllowMutations(t, true)
	const fakeValue = "totally-not-a-real-credential-marker-9f3e7a"
	resps := serveLines(t, s,
		callToolReq(1, "secret_add", map[string]interface{}{
			"kind": "secret", "name": "mcp-adapter-test-secret",
			"fields": map[string]interface{}{"value": fakeValue},
		}),
		callToolReq(2, "secret_list", map[string]interface{}{}),
	)

	addText, isErr := toolText(t, respByID(t, resps, "1"))
	if isErr {
		t.Fatalf("secret_add failed: %s", addText)
	}
	if strings.Contains(addText, fakeValue) {
		t.Fatalf("secret_add response leaked the value: %s", addText)
	}

	listText, isErr := toolText(t, respByID(t, resps, "2"))
	if isErr {
		t.Fatalf("secret_list failed: %s", listText)
	}
	if strings.Contains(listText, fakeValue) {
		t.Fatalf("secret_list response leaked the value: %s", listText)
	}
}

// TestNodeListCategoryFilter: node_list's category parameter matches the
// derived category (nodeCategory(t)), not a raw type-string prefix — chat's
// own list_node_types (deliberately not adapted; see the plan's Dedup
// section) does naive prefix matching via strings.HasPrefix(type,
// category+"."). gemini.* types are registered in this default build (no
// social tag needed — Gemini is browser-automation, not a gated social
// platform) and nodeCategory maps them to "browser/social", even though no
// type literally starts with "browser/social." — a naive prefix matcher
// could never find them under that category name at all, only a derived
// lookup can.
func TestNodeListCategoryFilter(t *testing.T) {
	s := newTestServer(t)
	resps := serveLines(t, s, callToolReq(1, "node_list", map[string]interface{}{"category": "browser/social"}))
	text, isErr := toolText(t, resps[0])
	if isErr {
		t.Fatalf("node_list failed: %s", text)
	}
	var nodes []struct {
		Type     string `json:"type"`
		Category string `json:"category"`
	}
	if err := json.Unmarshal([]byte(text), &nodes); err != nil {
		t.Fatalf("unmarshal node_list: %v", err)
	}
	if len(nodes) == 0 {
		t.Fatal("expected at least one browser/social-category node type (gemini.*)")
	}
	sawGemini := false
	for _, n := range nodes {
		if n.Category != "browser/social" {
			t.Errorf("node %s has category %q, want browser/social (filter should have excluded it)", n.Type, n.Category)
		}
		if strings.HasPrefix(n.Type, "gemini.") {
			sawGemini = true
		}
	}
	if !sawGemini {
		t.Errorf("expected gemini.* types in browser/social-category results, got %v", nodes)
	}

	// A raw type-prefix string that is NOT itself a real category (the bug
	// class chat's own naive prefix-matching version has) must return
	// nothing — proving this matches derived category, not type prefix.
	resps = serveLines(t, s, callToolReq(2, "node_list", map[string]interface{}{"category": "gemini"}))
	text, isErr = toolText(t, resps[0])
	if isErr {
		t.Fatalf("node_list failed: %s", text)
	}
	if strings.TrimSpace(text) != "[]" {
		t.Errorf(`node_list category:"gemini" = %s, want [] (gemini is a type prefix, not a category)`, text)
	}
}
