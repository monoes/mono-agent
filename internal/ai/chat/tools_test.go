package chat

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// setupTestDB creates an in-memory SQLite database with the workflow tables.
func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	schema := `
	CREATE TABLE workflows (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		description TEXT,
		is_active INTEGER NOT NULL DEFAULT 0,
		version INTEGER NOT NULL DEFAULT 1,
		profile_id TEXT NOT NULL DEFAULT 'default',
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);
	CREATE TABLE workflow_nodes (
		id TEXT PRIMARY KEY,
		workflow_id TEXT NOT NULL,
		node_type TEXT NOT NULL,
		name TEXT NOT NULL,
		config TEXT NOT NULL DEFAULT '{}',
		position_x REAL NOT NULL DEFAULT 0,
		position_y REAL NOT NULL DEFAULT 0,
		disabled INTEGER NOT NULL DEFAULT 0,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);
	CREATE TABLE workflow_connections (
		id TEXT PRIMARY KEY,
		workflow_id TEXT NOT NULL,
		source_node_id TEXT NOT NULL,
		source_handle TEXT NOT NULL,
		target_node_id TEXT NOT NULL,
		target_handle TEXT NOT NULL,
		position INTEGER NOT NULL DEFAULT 0
	);`

	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("create tables: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO workflows (id, name, profile_id) VALUES ('wf-1', 'Test Workflow', 'default')`,
	); err != nil {
		t.Fatalf("insert default workflow: %v", err)
	}

	t.Cleanup(func() { db.Close() })
	return db
}

func TestGetWorkflowState(t *testing.T) {
	db := setupTestDB(t)
	ct := NewCanvasTools(db)

	wfID := "wf-1"

	// Insert test nodes
	_, err := db.Exec(
		`INSERT INTO workflow_nodes (id, workflow_id, node_type, name, config, position_x, position_y, disabled)
		 VALUES ('n1', ?, 'trigger.manual', 'Start', '{}', 100, 200, 0)`, wfID)
	if err != nil {
		t.Fatalf("insert node 1: %v", err)
	}
	_, err = db.Exec(
		`INSERT INTO workflow_nodes (id, workflow_id, node_type, name, config, position_x, position_y, disabled)
		 VALUES ('n2', ?, 'core.set', 'Set Fields', '{"key":"value"}', 300, 200, 0)`, wfID)
	if err != nil {
		t.Fatalf("insert node 2: %v", err)
	}

	// Insert a connection
	_, err = db.Exec(
		`INSERT INTO workflow_connections (id, workflow_id, source_node_id, source_handle, target_node_id, target_handle, position)
		 VALUES ('c1', ?, 'n1', 'main', 'n2', 'main', 0)`, wfID)
	if err != nil {
		t.Fatalf("insert connection: %v", err)
	}

	result, err := ct.Execute("get_workflow_state", `{"workflow_id":"wf-1"}`)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	var state struct {
		WorkflowID  string          `json:"workflow_id"`
		Nodes       []nodeRow       `json:"nodes"`
		Connections []connectionRow `json:"connections"`
	}
	if err := json.Unmarshal([]byte(result), &state); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if state.WorkflowID != wfID {
		t.Errorf("workflow_id = %q, want %q", state.WorkflowID, wfID)
	}
	if len(state.Nodes) != 2 {
		t.Fatalf("got %d nodes, want 2", len(state.Nodes))
	}
	if len(state.Connections) != 1 {
		t.Fatalf("got %d connections, want 1", len(state.Connections))
	}
	if state.Connections[0].SourceNodeID != "n1" || state.Connections[0].TargetNodeID != "n2" {
		t.Errorf("connection mismatch: source=%s target=%s", state.Connections[0].SourceNodeID, state.Connections[0].TargetNodeID)
	}
}

func TestCreateNodes(t *testing.T) {
	db := setupTestDB(t)
	ct := NewCanvasTools(db)

	args := `{
		"workflow_id": "wf-1",
		"nodes": [
			{"node_type": "trigger.manual", "name": "Start", "position_x": 100, "position_y": 200},
			{"node_type": "core.set", "name": "Transform", "config": {"field": "val"}, "position_x": 300, "position_y": 200}
		]
	}`

	result, err := ct.Execute("create_nodes", args)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	var res struct {
		CreatedNodeIDs []string `json:"created_node_ids"`
	}
	if err := json.Unmarshal([]byte(result), &res); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if len(res.CreatedNodeIDs) != 2 {
		t.Fatalf("got %d IDs, want 2", len(res.CreatedNodeIDs))
	}

	// Verify nodes exist in DB
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM workflow_nodes WHERE workflow_id = 'wf-1'`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 2 {
		t.Errorf("DB has %d nodes, want 2", count)
	}

	// Verify the second node has config
	var configStr string
	if err := db.QueryRow(`SELECT config FROM workflow_nodes WHERE id = ?`, res.CreatedNodeIDs[1]).Scan(&configStr); err != nil {
		t.Fatalf("query config: %v", err)
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal([]byte(configStr), &cfg); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	if cfg["field"] != "val" {
		t.Errorf("config field = %v, want 'val'", cfg["field"])
	}
}

func TestUpdateNodeConfig(t *testing.T) {
	db := setupTestDB(t)
	ct := NewCanvasTools(db)

	// Create a node first
	_, err := db.Exec(
		`INSERT INTO workflow_nodes (id, workflow_id, node_type, name, config, position_x, position_y, disabled)
		 VALUES ('n1', 'wf-1', 'core.set', 'Set', '{"existing":"keep"}', 0, 0, 0)`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	result, err := ct.Execute("update_node_config", `{"workflow_id":"wf-1","node_id":"n1","config":{"new_key":"new_val"}}`)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	var res struct {
		NodeID string                 `json:"node_id"`
		Config map[string]interface{} `json:"config"`
	}
	if err := json.Unmarshal([]byte(result), &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if res.Config["existing"] != "keep" {
		t.Errorf("existing key lost: %v", res.Config)
	}
	if res.Config["new_key"] != "new_val" {
		t.Errorf("new key missing: %v", res.Config)
	}

	// Verify in DB
	var configStr string
	if err := db.QueryRow(`SELECT config FROM workflow_nodes WHERE id = 'n1'`).Scan(&configStr); err != nil {
		t.Fatalf("query: %v", err)
	}
	var dbCfg map[string]interface{}
	if err := json.Unmarshal([]byte(configStr), &dbCfg); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if dbCfg["existing"] != "keep" || dbCfg["new_key"] != "new_val" {
		t.Errorf("DB config mismatch: %v", dbCfg)
	}
}

func TestConnectNodes(t *testing.T) {
	db := setupTestDB(t)
	ct := NewCanvasTools(db)

	// Create two nodes
	for _, q := range []string{
		`INSERT INTO workflow_nodes (id, workflow_id, node_type, name, config) VALUES ('n1', 'wf-1', 'trigger.manual', 'Start', '{}')`,
		`INSERT INTO workflow_nodes (id, workflow_id, node_type, name, config) VALUES ('n2', 'wf-1', 'core.set', 'Set', '{}')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	result, err := ct.Execute("connect_nodes", `{
		"workflow_id": "wf-1",
		"source_node_id": "n1",
		"source_handle": "main",
		"target_node_id": "n2",
		"target_handle": "main"
	}`)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	var res struct {
		ConnectionID string `json:"connection_id"`
	}
	if err := json.Unmarshal([]byte(result), &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if res.ConnectionID == "" {
		t.Fatal("expected non-empty connection_id")
	}

	// Verify in DB
	var count int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM workflow_connections WHERE workflow_id='wf-1' AND source_node_id='n1' AND target_node_id='n2'`).Scan(&count); err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 1 {
		t.Errorf("got %d connections, want 1", count)
	}
}

func TestDisconnectNodes(t *testing.T) {
	db := setupTestDB(t)
	ct := NewCanvasTools(db)

	// Insert a connection
	_, err := db.Exec(
		`INSERT INTO workflow_connections (id, workflow_id, source_node_id, source_handle, target_node_id, target_handle, position)
		 VALUES ('c1', 'wf-1', 'n1', 'main', 'n2', 'main', 0)`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	result, err := ct.Execute("disconnect_nodes", `{"workflow_id":"wf-1","source_node_id":"n1","target_node_id":"n2"}`)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	var res struct {
		DeletedCount int64 `json:"deleted_count"`
	}
	if err := json.Unmarshal([]byte(result), &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if res.DeletedCount != 1 {
		t.Errorf("deleted_count = %d, want 1", res.DeletedCount)
	}

	// Verify gone
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM workflow_connections WHERE workflow_id='wf-1'`).Scan(&count); err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 0 {
		t.Errorf("still %d connections, want 0", count)
	}
}

func TestDeleteNodes(t *testing.T) {
	backupDir := t.TempDir()
	old := aiBackupDir
	aiBackupDir = func() (string, error) { return backupDir, nil }
	t.Cleanup(func() { aiBackupDir = old })

	db := setupTestDB(t)
	ct := NewCanvasTools(db)

	// Create a node and a connection involving it
	_, err := db.Exec(
		`INSERT INTO workflow_nodes (id, workflow_id, node_type, name, config) VALUES ('n1', 'wf-1', 'trigger.manual', 'Start', '{}')`)
	if err != nil {
		t.Fatalf("insert node: %v", err)
	}
	_, err = db.Exec(
		`INSERT INTO workflow_nodes (id, workflow_id, node_type, name, config) VALUES ('n2', 'wf-1', 'core.set', 'Set', '{}')`)
	if err != nil {
		t.Fatalf("insert node: %v", err)
	}
	_, err = db.Exec(
		`INSERT INTO workflow_connections (id, workflow_id, source_node_id, source_handle, target_node_id, target_handle, position)
		 VALUES ('c1', 'wf-1', 'n1', 'main', 'n2', 'main', 0)`)
	if err != nil {
		t.Fatalf("insert conn: %v", err)
	}

	result, err := ct.Execute("delete_nodes", `{"workflow_id":"wf-1","node_ids":["n1"]}`)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	var res struct {
		DeletedNodeIDs []string `json:"deleted_node_ids"`
	}
	if err := json.Unmarshal([]byte(result), &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(res.DeletedNodeIDs) != 1 || res.DeletedNodeIDs[0] != "n1" {
		t.Errorf("unexpected deleted IDs: %v", res.DeletedNodeIDs)
	}

	// Node should be gone
	var nodeCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM workflow_nodes WHERE id='n1'`).Scan(&nodeCount); err != nil {
		t.Fatalf("query: %v", err)
	}
	if nodeCount != 0 {
		t.Errorf("node still exists")
	}

	// Connection should also be gone (cascade)
	var connCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM workflow_connections WHERE source_node_id='n1' OR target_node_id='n1'`).Scan(&connCount); err != nil {
		t.Fatalf("query: %v", err)
	}
	if connCount != 0 {
		t.Errorf("connections still exist for deleted node")
	}

	// n2 should still exist
	var n2Count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM workflow_nodes WHERE id='n2'`).Scan(&n2Count); err != nil {
		t.Fatalf("query: %v", err)
	}
	if n2Count != 1 {
		t.Errorf("n2 was incorrectly deleted")
	}
}

func TestListAvailableNodes(t *testing.T) {
	db := setupTestDB(t)
	ct := NewCanvasTools(db)
	ct.SetNodeTypes([]NodeTypeInfo{
		{Type: "trigger.manual", Label: "Manual Trigger", Category: "trigger", Description: "Start manually"},
		{Type: "trigger.schedule", Label: "Schedule", Category: "trigger", Description: "Cron trigger"},
		{Type: "if", Label: "If", Category: "control", Description: "Branch"},
	})

	// No category filter
	result, err := ct.Execute("list_available_nodes", `{}`)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	var res struct {
		NodeTypes []map[string]interface{} `json:"node_types"`
	}
	if err := json.Unmarshal([]byte(result), &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(res.NodeTypes) == 0 {
		t.Fatal("expected non-empty node_types")
	}

	// With category filter
	result2, err := ct.Execute("list_available_nodes", `{"category":"trigger"}`)
	if err != nil {
		t.Fatalf("execute with filter: %v", err)
	}

	var res2 struct {
		NodeTypes []map[string]interface{} `json:"node_types"`
	}
	if err := json.Unmarshal([]byte(result2), &res2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, nt := range res2.NodeTypes {
		if nt["category"] != "trigger" {
			t.Errorf("expected category=trigger, got %v", nt["category"])
		}
	}
	if len(res2.NodeTypes) == 0 {
		t.Fatal("expected at least one trigger node type")
	}
}

func TestToolDefs(t *testing.T) {
	ct := NewCanvasTools(nil)
	defs := ct.ToolDefs()
	if len(defs) != 8 {
		t.Fatalf("got %d tool defs, want 8", len(defs))
	}

	names := make(map[string]bool)
	for _, d := range defs {
		if d.Type != "function" {
			t.Errorf("tool %s has type %q, want 'function'", d.Function.Name, d.Type)
		}
		names[d.Function.Name] = true
	}

	expected := []string{
		"get_workflow_state", "create_workflow", "create_nodes", "update_node_config",
		"delete_nodes", "connect_nodes", "disconnect_nodes", "list_available_nodes",
	}
	for _, name := range expected {
		if !names[name] {
			t.Errorf("missing tool def: %s", name)
		}
	}
}

func TestExecuteUnknownTool(t *testing.T) {
	ct := NewCanvasTools(nil)
	_, err := ct.Execute("nonexistent", `{}`)
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
}

// TestGetWorkflowStateRedactsSecrets is the RB3 leak regression test: node
// config values under credential-like keys must never cross the LLM
// boundary in get_workflow_state output.
func TestGetWorkflowStateRedactsSecrets(t *testing.T) {
	db := setupTestDB(t)
	ct := NewCanvasTools(db)

	_, err := db.Exec(
		`INSERT INTO workflow_nodes (id, workflow_id, node_type, name, config) VALUES ('n1', 'wf-1', 'ai.chat', 'Chat', '{"model":"gpt-4o","api_key":"sk-live-SUPERSECRET","password":"hunter2","nested":{"token":"tok-123"}}')`)
	if err != nil {
		t.Fatalf("insert node: %v", err)
	}

	result, err := ct.Execute("get_workflow_state", `{"workflow_id":"wf-1"}`)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.Contains(result, "sk-live-SUPERSECRET") || strings.Contains(result, "hunter2") || strings.Contains(result, "tok-123") {
		t.Errorf("workflow state leaked a secret: %s", result)
	}
	if !strings.Contains(result, "***") {
		t.Errorf("expected redaction marker *** in state: %s", result)
	}
	if !strings.Contains(result, "gpt-4o") {
		t.Errorf("non-secret config value was over-redacted: %s", result)
	}
}

// TestDeleteNodesSnapshotsToSidecar verifies delete_nodes persists the
// removed nodes+connections to <id>.ai-backup.json (last 3 kept) AND
// returns them in the tool result, so a wrong delete is recoverable.
func TestDeleteNodesSnapshotsToSidecar(t *testing.T) {
	backupDir := t.TempDir()
	old := aiBackupDir
	aiBackupDir = func() (string, error) { return backupDir, nil }
	t.Cleanup(func() { aiBackupDir = old })

	db := setupTestDB(t)
	ct := NewCanvasTools(db)

	if _, err := db.Exec(
		`INSERT INTO workflow_nodes (id, workflow_id, node_type, name, config) VALUES ('n1', 'wf-1', 'ai.chat', 'Chat', '{"model":"gpt-4o"}')`); err != nil {
		t.Fatalf("insert node: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO workflow_connections (id, workflow_id, source_node_id, source_handle, target_node_id, target_handle, position)
		 VALUES ('c1', 'wf-1', 'n1', 'main', 'n1', 'main', 0)`); err != nil {
		t.Fatalf("insert conn: %v", err)
	}

	result, err := ct.Execute("delete_nodes", `{"workflow_id":"wf-1","node_ids":["n1"]}`)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	// Tool result carries the removed entities.
	if !strings.Contains(result, `"id":"n1"`) || !strings.Contains(result, `"id":"c1"`) {
		t.Errorf("result missing removed node/connection: %s", result)
	}

	// Sidecar contains the snapshot.
	raw, err := os.ReadFile(filepath.Join(backupDir, "wf-1.ai-backup.json"))
	if err != nil {
		t.Fatalf("read sidecar: %v", err)
	}
	var backup deleteBackup
	if err := json.Unmarshal(raw, &backup); err != nil {
		t.Fatalf("parse sidecar: %v", err)
	}
	if backup.WorkflowID != "wf-1" || len(backup.Snapshots) != 1 {
		t.Fatalf("backup = %+v, want wf-1 with 1 snapshot", backup)
	}
	snap := backup.Snapshots[0]
	if len(snap.Nodes) != 1 || snap.Nodes[0].ID != "n1" || snap.Nodes[0].NodeType != "ai.chat" {
		t.Errorf("snapshot nodes = %+v, want n1/ai.chat", snap.Nodes)
	}
	if len(snap.Connections) != 1 || snap.Connections[0].ID != "c1" {
		t.Errorf("snapshot connections = %+v, want c1", snap.Connections)
	}

	// Rotation: 3 more deletes keep only the last 3 snapshots.
	for i := 0; i < 3; i++ {
		nodeID := fmt.Sprintf("rot%d", i)
		if _, err := db.Exec(
			`INSERT INTO workflow_nodes (id, workflow_id, node_type, name, config) VALUES (?, 'wf-1', 'core.set', 'X', '{}')`, nodeID); err != nil {
			t.Fatalf("insert rot node: %v", err)
		}
		if _, err := ct.Execute("delete_nodes", fmt.Sprintf(`{"workflow_id":"wf-1","node_ids":[%q]}`, nodeID)); err != nil {
			t.Fatalf("delete rot %d: %v", i, err)
		}
	}
	raw, err = os.ReadFile(filepath.Join(backupDir, "wf-1.ai-backup.json"))
	if err != nil {
		t.Fatalf("reread sidecar: %v", err)
	}
	if err := json.Unmarshal(raw, &backup); err != nil {
		t.Fatalf("reparse sidecar: %v", err)
	}
	if len(backup.Snapshots) != maxAIBackupSnapshots {
		t.Errorf("snapshots = %d, want capped at %d", len(backup.Snapshots), maxAIBackupSnapshots)
	}
	if backup.Snapshots[0].Nodes[0].ID != "rot2" {
		t.Errorf("newest snapshot = %+v, want rot2 first", backup.Snapshots[0].Nodes)
	}
}

// TestDeleteNodesBackupFailureAbortsDelete verifies fail-closed behavior:
// if the pre-delete snapshot cannot be persisted, nothing is deleted.
func TestDeleteNodesBackupFailureAbortsDelete(t *testing.T) {
	old := aiBackupDir
	aiBackupDir = func() (string, error) { return "", fmt.Errorf("no home") }
	t.Cleanup(func() { aiBackupDir = old })

	db := setupTestDB(t)
	ct := NewCanvasTools(db)
	if _, err := db.Exec(
		`INSERT INTO workflow_nodes (id, workflow_id, node_type, name, config) VALUES ('n1', 'wf-1', 'core.set', 'X', '{}')`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	if _, err := ct.Execute("delete_nodes", `{"workflow_id":"wf-1","node_ids":["n1"]}`); err == nil {
		t.Fatal("expected error when backup cannot be persisted")
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM workflow_nodes WHERE id='n1'`).Scan(&count); err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 1 {
		t.Error("node was deleted despite backup failure")
	}
}

// TestMarshalJSONTruncatesOversizedResults verifies every tool result is
// capped at 32KB with an explicit truncation marker.
func TestMarshalJSONTruncatesOversizedResults(t *testing.T) {
	big := map[string]interface{}{"blob": strings.Repeat("x", 64*1024)}
	out, err := marshalJSON(big)
	if err != nil {
		t.Fatalf("marshalJSON: %v", err)
	}
	if len(out) > maxToolResultBytes+len(truncatedResultMarker) {
		t.Errorf("result len = %d, want <= %d", len(out), maxToolResultBytes+len(truncatedResultMarker))
	}
	if !strings.HasSuffix(out, truncatedResultMarker) {
		t.Error("oversized result missing ...[truncated] marker")
	}

	small, err := marshalJSON(map[string]interface{}{"a": 1})
	if err != nil {
		t.Fatalf("marshalJSON small: %v", err)
	}
	if strings.Contains(small, truncatedResultMarker) {
		t.Error("small result must not be truncated")
	}
}

// TestCreateNodesRejectsUnknownNodeType verifies create_nodes validates
// node_type against the provided node-type registry.
func TestCreateNodesRejectsUnknownNodeType(t *testing.T) {
	db := setupTestDB(t)
	ct := NewCanvasTools(db)
	ct.SetNodeTypes([]NodeTypeInfo{
		{Type: "trigger.manual", Label: "Manual", Category: "trigger", Description: ""},
		{Type: "core.set", Label: "Set", Category: "control", Description: ""},
	})

	_, err := ct.Execute("create_nodes", `{"workflow_id":"wf-1","nodes":[{"node_type":"not.a.real.node","name":"X"}]}`)
	if err == nil {
		t.Fatal("expected error for unknown node_type")
	}
	if !strings.Contains(err.Error(), "unknown node_type") {
		t.Errorf("error = %v, want unknown node_type rejection", err)
	}

	// Known types still succeed.
	if _, err := ct.Execute("create_nodes", `{"workflow_id":"wf-1","nodes":[{"node_type":"core.set","name":"OK"}]}`); err != nil {
		t.Fatalf("valid node_type rejected: %v", err)
	}

	// No registry provided (e.g. CLI without node types): validation skips.
	ct2 := NewCanvasTools(db)
	if _, err := ct2.Execute("create_nodes", `{"workflow_id":"wf-1","nodes":[{"node_type":"anything","name":"X"}]}`); err != nil {
		t.Fatalf("validation should skip when registry is empty: %v", err)
	}
}

// TestConnectNodesRequiresExistingEndpoints verifies connect_nodes refuses
// to wire connections to node ids that don't exist in the workflow.
func TestConnectNodesRequiresExistingEndpoints(t *testing.T) {
	db := setupTestDB(t)
	ct := NewCanvasTools(db)
	if _, err := db.Exec(
		`INSERT INTO workflow_nodes (id, workflow_id, node_type, name, config) VALUES ('n1', 'wf-1', 'trigger.manual', 'Start', '{}')`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	_, err := ct.Execute("connect_nodes", `{
		"workflow_id": "wf-1",
		"source_node_id": "n1",
		"source_handle": "main",
		"target_node_id": "ghost",
		"target_handle": "main"
	}`)
	if err == nil {
		t.Fatal("expected error connecting to a nonexistent node")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error = %v, want it to name the missing node", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM workflow_connections WHERE workflow_id='wf-1'`).Scan(&count); err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 0 {
		t.Error("connection was inserted despite missing endpoint")
	}
}

// TestMutatingToolsRejectPlaceholderWorkflowIDs verifies the "general"/
// "draft" placeholders are read-only: every mutating tool must refuse them
// so the model is forced through create_workflow first.
func TestMutatingToolsRejectPlaceholderWorkflowIDs(t *testing.T) {
	db := setupTestDB(t)
	ct := NewCanvasTools(db)

	mutations := []struct {
		tool, args string
	}{
		{"create_nodes", `{"workflow_id":"general","nodes":[{"node_type":"core.set","name":"x"}]}`},
		{"create_nodes", `{"workflow_id":"draft","nodes":[{"node_type":"core.set","name":"x"}]}`},
		{"update_node_config", `{"workflow_id":"general","node_id":"n1","config":{"a":1}}`},
		{"update_node_config", `{"workflow_id":"draft","node_id":"n1","config":{"a":1}}`},
		{"delete_nodes", `{"workflow_id":"general","node_ids":["n1"]}`},
		{"delete_nodes", `{"workflow_id":"draft","node_ids":["n1"]}`},
		{"connect_nodes", `{"workflow_id":"general","source_node_id":"a","source_handle":"main","target_node_id":"b","target_handle":"main"}`},
		{"connect_nodes", `{"workflow_id":"draft","source_node_id":"a","source_handle":"main","target_node_id":"b","target_handle":"main"}`},
		{"disconnect_nodes", `{"workflow_id":"general","source_node_id":"a","target_node_id":"b"}`},
		{"disconnect_nodes", `{"workflow_id":"draft","source_node_id":"a","target_node_id":"b"}`},
	}
	for _, m := range mutations {
		if _, err := ct.Execute(m.tool, m.args); err == nil {
			t.Errorf("%s on placeholder %q: expected error, got nil", m.tool, jsonArgWorkflowID(t, m.args))
		}
	}

	// Read-only access to placeholders remains allowed (state is empty).
	if _, err := ct.Execute("get_workflow_state", `{"workflow_id":"general"}`); err != nil {
		t.Errorf("get_workflow_state on placeholder: unexpected error: %v", err)
	}
}

func jsonArgWorkflowID(t *testing.T, args string) string {
	t.Helper()
	var a struct {
		WorkflowID string `json:"workflow_id"`
	}
	_ = json.Unmarshal([]byte(args), &a)
	return a.WorkflowID
}

// TestCanvasToolsRejectCrossProfileWorkflow is a regression test: a CanvasTools
// instance scoped to one profile must not be able to read or mutate a workflow
// that belongs to a different profile.
func TestCanvasToolsRejectCrossProfileWorkflow(t *testing.T) {
	db := setupTestDB(t)
	if _, err := db.Exec(
		`INSERT INTO workflows (id, name, profile_id) VALUES ('wf-other', 'Other Profile Workflow', 'other-profile')`,
	); err != nil {
		t.Fatalf("insert other-profile workflow: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO workflow_nodes (id, workflow_id, node_type, name, config) VALUES ('n1', 'wf-other', 'trigger.manual', 'Start', '{}')`,
	); err != nil {
		t.Fatalf("insert node: %v", err)
	}

	ct := NewCanvasTools(db) // defaults to profileID "default"

	if _, err := ct.Execute("get_workflow_state", `{"workflow_id":"wf-other"}`); err == nil {
		t.Error("get_workflow_state: expected error for cross-profile workflow, got nil")
	}
	if _, err := ct.Execute("create_nodes", `{"workflow_id":"wf-other","nodes":[{"node_type":"core.set","name":"x"}]}`); err == nil {
		t.Error("create_nodes: expected error for cross-profile workflow, got nil")
	}
	if _, err := ct.Execute("update_node_config", `{"workflow_id":"wf-other","node_id":"n1","config":{"a":1}}`); err == nil {
		t.Error("update_node_config: expected error for cross-profile workflow, got nil")
	}
	if _, err := ct.Execute("delete_nodes", `{"workflow_id":"wf-other","node_ids":["n1"]}`); err == nil {
		t.Error("delete_nodes: expected error for cross-profile workflow, got nil")
	}
	if _, err := ct.Execute("connect_nodes", `{"workflow_id":"wf-other","source_node_id":"n1","source_handle":"main","target_node_id":"n1","target_handle":"main"}`); err == nil {
		t.Error("connect_nodes: expected error for cross-profile workflow, got nil")
	}
	if _, err := ct.Execute("disconnect_nodes", `{"workflow_id":"wf-other","source_node_id":"n1","target_node_id":"n1"}`); err == nil {
		t.Error("disconnect_nodes: expected error for cross-profile workflow, got nil")
	}

	// Sanity: the same profile's own workflow (wf-1, inserted by setupTestDB) is still accessible.
	if _, err := ct.Execute("get_workflow_state", `{"workflow_id":"wf-1"}`); err != nil {
		t.Errorf("get_workflow_state on own workflow: unexpected error: %v", err)
	}

	// Placeholder IDs used before a real workflow exists must always be allowed.
	if _, err := ct.Execute("get_workflow_state", `{"workflow_id":"general"}`); err != nil {
		t.Errorf("get_workflow_state on placeholder 'general': unexpected error: %v", err)
	}
}

// TestCreateNodesTypeAlias is a regression test for a bug found testing both
// Claude and Codex live against --canvas: both independently sent "type"
// instead of the schema's "node_type" key, which the old code silently
// accepted and inserted as an empty node_type — a broken, non-executable
// node with a fully successful-looking tool_result. "type" must resolve the
// same as "node_type", and a genuinely missing/unknown type must now error
// instead of silently persisting.
func TestCreateNodesTypeAlias(t *testing.T) {
	db := setupTestDB(t)
	ct := NewCanvasTools(db)

	// "type" alias resolves exactly like "node_type" would.
	result, err := ct.Execute("create_nodes", `{
		"workflow_id": "wf-1",
		"nodes": [{"type": "http.request", "name": "Fetch", "position_x": 0, "position_y": 0}]
	}`)
	if err != nil {
		t.Fatalf("execute with 'type' alias: %v", err)
	}
	var res struct {
		CreatedNodeIDs []string `json:"created_node_ids"`
	}
	if err := json.Unmarshal([]byte(result), &res); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(res.CreatedNodeIDs) != 1 {
		t.Fatalf("got %d node IDs, want 1", len(res.CreatedNodeIDs))
	}
	var nodeType, name string
	if err := db.QueryRow(`SELECT node_type, name FROM workflow_nodes WHERE id = ?`, res.CreatedNodeIDs[0]).Scan(&nodeType, &name); err != nil {
		t.Fatalf("query inserted node: %v", err)
	}
	if nodeType != "http.request" {
		t.Errorf("node_type = %q, want %q — 'type' alias was not resolved", nodeType, "http.request")
	}
	if name != "Fetch" {
		t.Errorf("name = %q, want %q", name, "Fetch")
	}

	// Neither key present: must error, not silently insert an empty type.
	if _, err := ct.Execute("create_nodes", `{
		"workflow_id": "wf-1",
		"nodes": [{"name": "Nameless", "position_x": 0, "position_y": 0}]
	}`); err == nil {
		t.Error("create_nodes with neither node_type nor type: expected error, got nil")
	}

	// Missing name defaults to the resolved type rather than erroring —
	// name is cosmetic, unlike node_type.
	result2, err := ct.Execute("create_nodes", `{
		"workflow_id": "wf-1",
		"nodes": [{"node_type": "core.set", "position_x": 0, "position_y": 0}]
	}`)
	if err != nil {
		t.Fatalf("execute with missing name: %v", err)
	}
	var res2 struct {
		CreatedNodeIDs []string `json:"created_node_ids"`
	}
	if err := json.Unmarshal([]byte(result2), &res2); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	var name2 string
	if err := db.QueryRow(`SELECT name FROM workflow_nodes WHERE id = ?`, res2.CreatedNodeIDs[0]).Scan(&name2); err != nil {
		t.Fatalf("query inserted node: %v", err)
	}
	if name2 != "core.set" {
		t.Errorf("name = %q, want %q (defaulted to node_type)", name2, "core.set")
	}
}

// TestCreateNodesRejectsUnknownType verifies validation against the known
// node registry when SetNodeTypes has been called (the real chat.go call
// path always sets it from the live node registry — see registryNodeTypes
// in cmd/monoagentcli/chat.go).
func TestCreateNodesRejectsUnknownType(t *testing.T) {
	db := setupTestDB(t)
	ct := NewCanvasTools(db)
	ct.SetNodeTypes([]NodeTypeInfo{
		{Type: "http.request", Label: "HTTP Request", Category: "http"},
		{Type: "core.set", Label: "Set", Category: "core"},
	})

	if _, err := ct.Execute("create_nodes", `{
		"workflow_id": "wf-1",
		"nodes": [{"node_type": "totally.madeup", "name": "Bogus", "position_x": 0, "position_y": 0}]
	}`); err == nil {
		t.Error("create_nodes with an unknown node_type: expected error, got nil")
	}

	// A known type still succeeds once the registry is populated.
	if _, err := ct.Execute("create_nodes", `{
		"workflow_id": "wf-1",
		"nodes": [{"node_type": "core.set", "name": "Real", "position_x": 0, "position_y": 0}]
	}`); err != nil {
		t.Errorf("create_nodes with a known node_type: unexpected error: %v", err)
	}
}

// TestCreateNodesAllowsTriggerTypes is a regression test: trigger.manual/
// schedule/webhook belong to a separate subsystem (internal/workflow's
// trigger_manager.go and validator.go, which recognize them by the same
// "trigger." prefix) that never appears in the registry list_available_nodes
// draws from. Found live: asking Claude to build a workflow via the general
// assistant, its trigger.manual node was rejected by an earlier version of
// this validation even though real workflows in the DB already contain one
// — the registry check must not apply to the trigger namespace.
func TestCreateNodesAllowsTriggerTypes(t *testing.T) {
	db := setupTestDB(t)
	ct := NewCanvasTools(db)
	ct.SetNodeTypes([]NodeTypeInfo{
		{Type: "http.request", Label: "HTTP Request", Category: "http"},
	})

	result, err := ct.Execute("create_nodes", `{
		"workflow_id": "wf-1",
		"nodes": [{"node_type": "trigger.manual", "name": "Start", "position_x": 0, "position_y": 0}]
	}`)
	if err != nil {
		t.Fatalf("create_nodes with trigger.manual: unexpected error: %v", err)
	}
	var res struct {
		CreatedNodeIDs []string `json:"created_node_ids"`
	}
	if err := json.Unmarshal([]byte(result), &res); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	var nodeType string
	if err := db.QueryRow(`SELECT node_type FROM workflow_nodes WHERE id = ?`, res.CreatedNodeIDs[0]).Scan(&nodeType); err != nil {
		t.Fatalf("query inserted node: %v", err)
	}
	if nodeType != "trigger.manual" {
		t.Errorf("node_type = %q, want %q", nodeType, "trigger.manual")
	}

	// A genuinely unknown type must still be rejected — the trigger
	// exemption is prefix-scoped, not a blanket bypass.
	if _, err := ct.Execute("create_nodes", `{
		"workflow_id": "wf-1",
		"nodes": [{"node_type": "totally.madeup", "name": "Bogus", "position_x": 0, "position_y": 0}]
	}`); err == nil {
		t.Error("create_nodes with an unknown non-trigger type: expected error, got nil")
	}
}
