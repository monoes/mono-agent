package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/storage"
	"github.com/monoes/mono-agent/internal/workflow"
)

// validWorkflowFile is a minimal, activation-valid workflow: a manual trigger
// feeding one core.set node.
const validWorkflowFile = `{
  "name": "test-wf",
  "nodes": [
    {"id": "trigger", "type": "trigger.manual", "name": "Manual", "position": {"x": 0, "y": 0}, "config": {}},
    {"id": "set", "type": "core.set", "name": "Set", "position": {"x": 1, "y": 0}, "config": {"fields": {}}}
  ],
  "connections": [
    {"id": "c1", "source": "trigger", "source_handle": "main", "target": "set", "target_handle": "main"}
  ]
}`

// cycleWorkflowFile: trigger → a → b → a (cycle).
const cycleWorkflowFile = `{
  "name": "cycle-wf",
  "nodes": [
    {"id": "trigger", "type": "trigger.manual", "name": "Manual", "position": {"x": 0, "y": 0}, "config": {}},
    {"id": "a", "type": "core.set", "name": "A", "position": {"x": 1, "y": 0}, "config": {}},
    {"id": "b", "type": "core.set", "name": "B", "position": {"x": 2, "y": 0}, "config": {}}
  ],
  "connections": [
    {"id": "c1", "source": "trigger", "source_handle": "main", "target": "a", "target_handle": "main"},
    {"id": "c2", "source": "a", "source_handle": "main", "target": "b", "target_handle": "main"},
    {"id": "c3", "source": "b", "source_handle": "main", "target": "a", "target_handle": "main"}
  ]
}`

// noTriggerWorkflowFile: valid structure but no trigger.* node.
const noTriggerWorkflowFile = `{
  "name": "no-trigger-wf",
  "nodes": [
    {"id": "set", "type": "core.set", "name": "Set", "position": {"x": 0, "y": 0}, "config": {}}
  ],
  "connections": []
}`

// handlelessWorkflowFile: connections without source_handle/target_handle,
// like the README flagship example — import accepts them and the engine
// routes on the "main" port, so validate must accept them too.
const handlelessWorkflowFile = `{
  "name": "handleless-wf",
  "nodes": [
    {"id": "trigger", "type": "trigger.schedule", "name": "Cron", "position": {"x": 0, "y": 0}, "config": {"cron": "0 0 9 * * *"}},
    {"id": "a", "type": "core.set", "name": "A", "position": {"x": 1, "y": 0}, "config": {}}
  ],
  "connections": [
    {"id": "c1", "source": "trigger", "target": "a"}
  ]
}`

// legacyHandlelessWorkflowFile: legacy export keys AND no connection
// handles — validate must normalize keys (like import) before checking.
const legacyHandlelessWorkflowFile = `{
  "name": "legacy-handleless-wf",
  "nodes": [
    {"id": "trigger", "node_type": "trigger.manual", "name": "Manual", "position_x": 0, "position_y": 0, "config": {}},
    {"id": "a", "node_type": "core.set", "name": "A", "position_x": 1, "position_y": 0, "config": {}}
  ],
  "connections": [
    {"id": "c1", "source_node_id": "trigger", "target_node_id": "a"}
  ]
}`

func writeTempWorkflow(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "workflow.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp workflow: %v", err)
	}
	return path
}

func runValidateCmd(t *testing.T, jsonOut bool, args ...string) (string, error) {
	t.Helper()
	cfg := &globalConfig{DBPath: filepath.Join(t.TempDir(), "unused.db"), JSONOutput: jsonOut}
	cmd := newWorkflowValidateCmd(cfg)
	cmd.SetArgs(args)
	var out bytes.Buffer
	cmd.SetOut(&out)
	err := cmd.Execute()
	return out.String(), err
}

func TestWorkflowValidateFile_Valid(t *testing.T) {
	path := writeTempWorkflow(t, validWorkflowFile)
	out, err := runValidateCmd(t, false, "--file", path)
	if err != nil {
		t.Fatalf("expected valid workflow, got error: %v", err)
	}
	if !strings.Contains(out, "Workflow is valid.") {
		t.Fatalf("expected success message, got: %q", out)
	}
}

func TestWorkflowValidateFile_ValidJSONShape(t *testing.T) {
	path := writeTempWorkflow(t, validWorkflowFile)
	out, err := runValidateCmd(t, true, "--file", path)
	if err != nil {
		t.Fatalf("expected valid workflow, got error: %v", err)
	}
	var got validationJSON
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v (out: %q)", err, out)
	}
	if !got.Valid {
		t.Fatalf("expected valid=true, got %+v", got)
	}
}

func TestWorkflowValidateFile_Cycle(t *testing.T) {
	path := writeTempWorkflow(t, cycleWorkflowFile)
	out, err := runValidateCmd(t, false, "--file", path)
	if err == nil {
		t.Fatalf("expected cycle to be rejected, got success (out: %q)", out)
	}
	if code := exitCodeFor(err); code != 3 {
		t.Fatalf("expected exit code 3 for cycle, got %d (%v)", code, err)
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cycle error, got: %v", err)
	}
}

func TestWorkflowValidateFile_MissingTrigger(t *testing.T) {
	path := writeTempWorkflow(t, noTriggerWorkflowFile)
	_, err := runValidateCmd(t, false, "--file", path)
	if err == nil {
		t.Fatal("expected missing trigger to be rejected")
	}
	if code := exitCodeFor(err); code != 3 {
		t.Fatalf("expected exit code 3 for missing trigger, got %d (%v)", code, err)
	}
	if !strings.Contains(err.Error(), "trigger") {
		t.Fatalf("expected trigger error, got: %v", err)
	}
}

func TestWorkflowValidateFile_MissingFileIsNotFound(t *testing.T) {
	_, err := runValidateCmd(t, false, "--file", filepath.Join(t.TempDir(), "nope.json"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if code := exitCodeFor(err); code != 2 {
		t.Fatalf("expected exit code 2 for missing file, got %d (%v)", code, err)
	}
}

func TestWorkflowValidateFile_InvalidJSONIsInvalidInput(t *testing.T) {
	path := writeTempWorkflow(t, "{not json")
	_, err := runValidateCmd(t, false, "--file", path)
	if err == nil {
		t.Fatal("expected error for unparseable JSON")
	}
	if code := exitCodeFor(err); code != 3 {
		t.Fatalf("expected exit code 3 for invalid JSON, got %d (%v)", code, err)
	}
}

func TestWorkflowValidateFile_HandlelessConnectionsDefaultToMain(t *testing.T) {
	path := writeTempWorkflow(t, handlelessWorkflowFile)
	out, err := runValidateCmd(t, false, "--file", path)
	if err != nil {
		t.Fatalf("expected handle-less connections to validate (handles default to main), got error: %v", err)
	}
	if !strings.Contains(out, "Workflow is valid.") {
		t.Fatalf("expected success message, got: %q", out)
	}
}

func TestWorkflowValidateFile_LegacyKeysNormalizedLikeImport(t *testing.T) {
	path := writeTempWorkflow(t, legacyHandlelessWorkflowFile)
	out, err := runValidateCmd(t, false, "--file", path)
	if err != nil {
		t.Fatalf("expected legacy-key workflow to validate via normalization, got error: %v", err)
	}
	if !strings.Contains(out, "Workflow is valid.") {
		t.Fatalf("expected success message, got: %q", out)
	}
}

func runNodeSchemaCmd(t *testing.T, nodeType string) (string, error) {
	t.Helper()
	cfg := &globalConfig{DBPath: filepath.Join(t.TempDir(), "unused.db")}
	cmd := newNodeCmd(cfg)
	cmd.SetArgs([]string{"schema", nodeType})
	var out bytes.Buffer
	cmd.SetOut(&out)
	err := cmd.Execute()
	return out.String(), err
}

func TestNodeSchemaCmd_KnownType(t *testing.T) {
	out, err := runNodeSchemaCmd(t, "core.if")
	if err != nil {
		t.Fatalf("node schema core.if: %v", err)
	}
	var schema map[string]interface{}
	if err := json.Unmarshal([]byte(out), &schema); err != nil {
		t.Fatalf("schema output is not valid JSON: %v (out: %q)", err, out)
	}
	fields, ok := schema["fields"].([]interface{})
	if !ok || len(fields) == 0 {
		t.Fatalf("expected non-empty fields array, got: %q", out)
	}
	first, _ := fields[0].(map[string]interface{})
	if first["key"] != "condition" {
		t.Fatalf("expected first field key %q, got %v", "condition", first["key"])
	}
}

func TestNodeSchemaCmd_UnknownTypeExit2(t *testing.T) {
	_, err := runNodeSchemaCmd(t, "bogus.does_not_exist")
	if err == nil {
		t.Fatal("expected error for unknown node type")
	}
	if code := exitCodeFor(err); code != 2 {
		t.Fatalf("expected exit code 2 for unknown node type, got %d (%v)", code, err)
	}
}

func TestDryRunSteps_PlanShape(t *testing.T) {
	wf := &workflow.Workflow{
		Name: "plan-wf",
		Nodes: []workflow.WorkflowNode{
			{ID: "trigger", Type: "trigger.manual", Config: map[string]interface{}{}},
			{ID: "set", Type: "core.set", Config: map[string]interface{}{}},
		},
		Connections: []workflow.WorkflowConnection{
			{ID: "c1", SourceNodeID: "trigger", SourceHandle: "main", TargetNodeID: "set", TargetHandle: "main"},
		},
	}
	steps, err := dryRunSteps(wf)
	if err != nil {
		t.Fatalf("dryRunSteps: %v", err)
	}
	if len(steps) != 2 {
		t.Fatalf("expected 2 steps, got %d (%+v)", len(steps), steps)
	}
	// Trigger nodes are seeded first in the topological queue.
	if steps[0].ID != "trigger" || steps[0].Order != 1 || steps[0].Type != "trigger.manual" {
		t.Fatalf("unexpected first step: %+v", steps[0])
	}
	if steps[1].ID != "set" || steps[1].Order != 2 || steps[1].Type != "core.set" {
		t.Fatalf("unexpected second step: %+v", steps[1])
	}
}

func TestDryRunSteps_RejectsCycle(t *testing.T) {
	wf := &workflow.Workflow{
		Name: "cycle-wf",
		Nodes: []workflow.WorkflowNode{
			{ID: "trigger", Type: "trigger.manual", Config: map[string]interface{}{}},
			{ID: "a", Type: "core.set", Config: map[string]interface{}{}},
			{ID: "b", Type: "core.set", Config: map[string]interface{}{}},
		},
		Connections: []workflow.WorkflowConnection{
			{ID: "c1", SourceNodeID: "trigger", SourceHandle: "main", TargetNodeID: "a", TargetHandle: "main"},
			{ID: "c2", SourceNodeID: "a", SourceHandle: "main", TargetNodeID: "b", TargetHandle: "main"},
			{ID: "c3", SourceNodeID: "b", SourceHandle: "main", TargetNodeID: "a", TargetHandle: "main"},
		},
	}
	if _, err := dryRunSteps(wf); err == nil {
		t.Fatal("expected cycle to be rejected")
	}
}

func TestDryRunSteps_MissingTrigger(t *testing.T) {
	wf := &workflow.Workflow{
		Name:  "no-trigger",
		Nodes: []workflow.WorkflowNode{{ID: "set", Type: "core.set", Config: map[string]interface{}{}}},
	}
	_, err := dryRunSteps(wf)
	if !errors.Is(err, workflow.ErrNoTriggerNode) {
		t.Fatalf("expected ErrNoTriggerNode, got %v", err)
	}
	if code := exitCodeFor(err); code != 3 {
		t.Fatalf("expected exit code 3 for missing trigger, got %d", code)
	}
}

func TestExitCodeFor(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, 0},
		{"notfound sentinel", errNotFound("thing %q missing", "x"), 2},
		{"wrapped invalid input", fmt.Errorf("context: %w", ErrInvalidInput), 3},
		{"auth sentinel", errAuthConnection("boom"), 4},
		{"engine workflow not found", fmt.Errorf("trigger: %w", workflow.ErrWorkflowNotFound), 2},
		{"engine execution not found", fmt.Errorf("poll: %w", workflow.ErrExecutionNotFound), 2},
		{"engine no trigger", fmt.Errorf("activate: %w", workflow.ErrNoTriggerNode), 3},
		{"engine cycle", fmt.Errorf("build: %w", workflow.ErrCycleDetected), 3},
		{"plain error", errors.New("anything else"), 1},
	}
	for _, tc := range cases {
		if got := exitCodeFor(tc.err); got != tc.want {
			t.Errorf("%s: exitCodeFor = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestTruncateOutputItems(t *testing.T) {
	small := workflow.Item{JSON: map[string]interface{}{"k": "v"}}
	big := workflow.Item{JSON: map[string]interface{}{"k": strings.Repeat("x", maxOutputItemBytes)}}

	got := truncateOutputItems([]workflow.Item{small, big})
	if len(got) != 2 {
		t.Fatalf("expected item count preserved (2), got %d", len(got))
	}
	if got[0].JSON["k"] != "v" {
		t.Fatalf("small item should pass through unchanged, got %+v", got[0])
	}
	truncated, _ := got[1].JSON["truncated"].(bool)
	if !truncated {
		t.Fatalf("big item should be truncated, got %+v", got[1])
	}
	if note, _ := got[1].JSON["note"].(string); note == "" {
		t.Fatal("truncated item should carry a note")
	}
}

// captureStdout is defined in people_status_test.go and reused below.

// legacyWorkflowFile is what `workflow export` used to emit: a marshaled
// *workflow.Workflow with "node_type", "position_x"/"position_y" on nodes
// and "source_node_id"/"target_node_id" on connections.
const legacyWorkflowFile = `{
  "id": "legacy-wf",
  "name": "legacy-wf",
  "nodes": [
    {"id": "trigger", "node_type": "trigger.manual", "name": "Manual", "config": {}, "position_x": 5, "position_y": 7},
    {"id": "set", "node_type": "core.set", "name": "Set", "config": {"fields": {}}, "position_x": 9, "position_y": 11}
  ],
  "connections": [
    {"id": "c1", "source_node_id": "trigger", "source_handle": "main", "target_node_id": "set", "target_handle": "main"}
  ]
}`

// runWorkflowSubcmd executes a `workflow …` subcommand with JSON output on
// and returns everything it printed to stdout.
func runWorkflowSubcmd(t *testing.T, cfg *globalConfig, args ...string) string {
	t.Helper()
	cmd := newWorkflowCmd(cfg)
	cmd.SetArgs(args)
	return captureStdout(t, func() {
		if err := cmd.Execute(); err != nil {
			t.Fatalf("workflow %v: %v", args, err)
		}
	})
}

// TestWorkflowExportImportRoundtrip guards RV6-3: `workflow export` must
// emit the documented WorkflowFile shape (type/source/target keys) that
// import parses natively, so export → wipe → import roundtrips nodes and
// connections losslessly. Export used to emit legacy-shape JSON that import
// silently parsed into empty node types and dangling connections.
func TestWorkflowExportImportRoundtrip(t *testing.T) {
	// Isolate the hybrid store's file half (~/.monoagent/workflows).
	home := t.TempDir()
	t.Setenv("HOME", home)

	dbPath := filepath.Join(t.TempDir(), "roundtrip.db")
	cfg := &globalConfig{DBPath: dbPath, JSONOutput: true, ProfileID: "default"}

	var created workflow.Workflow
	if err := json.Unmarshal([]byte(runWorkflowSubcmd(t, cfg, "create", "roundtrip-wf", "--description", "roundtrip")), &created); err != nil {
		t.Fatalf("parse create output: %v", err)
	}
	wfID := created.ID

	addNode := func(nodeType, name string, x, y float64) string {
		t.Helper()
		out := runWorkflowSubcmd(t, cfg, "node", "add", wfID,
			"--type", nodeType, "--name", name,
			"--x", fmt.Sprintf("%v", x), "--y", fmt.Sprintf("%v", y))
		var node workflow.WorkflowNode
		if err := json.Unmarshal([]byte(out), &node); err != nil {
			t.Fatalf("parse node add output: %v (out: %q)", err, out)
		}
		return node.ID
	}
	n1 := addNode("trigger.manual", "Trigger", 0, 0)
	n2 := addNode("core.set", "Set", 100, 200)
	n3 := addNode("core.set", "Set2", 300, 400)
	runWorkflowSubcmd(t, cfg, "connect", wfID, "--from", n1, "--to", n2)
	runWorkflowSubcmd(t, cfg, "connect", wfID, "--from", n2, "--to", n3)

	// Export must emit the native WorkflowFile shape, not legacy keys.
	exported := runWorkflowSubcmd(t, cfg, "export", wfID)
	var exportedFile workflow.WorkflowFile
	if err := json.Unmarshal([]byte(exported), &exportedFile); err != nil {
		t.Fatalf("parse export output: %v", err)
	}
	if len(exportedFile.Nodes) != 3 || len(exportedFile.Connections) != 2 {
		t.Fatalf("expected 3 nodes / 2 connections in export, got %d/%d", len(exportedFile.Nodes), len(exportedFile.Connections))
	}
	for _, n := range exportedFile.Nodes {
		if n.Type == "" {
			t.Fatalf("exported node %q has an empty type — legacy shape leaked", n.ID)
		}
	}
	for _, key := range []string{"node_type", "position_x", "source_node_id", "target_node_id"} {
		if strings.Contains(exported, `"`+key+`"`) {
			t.Fatalf("export must not emit legacy key %q", key)
		}
	}

	// Wipe both halves of the hybrid store, then import the export.
	db, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	for _, stmt := range []string{
		`DELETE FROM workflow_connections WHERE workflow_id = ?`,
		`DELETE FROM workflow_nodes WHERE workflow_id = ?`,
		`DELETE FROM workflows WHERE id = ?`,
	} {
		if _, err := db.DB.Exec(stmt, wfID); err != nil {
			t.Fatalf("wipe (%s): %v", stmt, err)
		}
	}
	if err := db.DB.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(home, ".monoagent", "workflows")); err != nil {
		t.Fatalf("wipe file store: %v", err)
	}

	exportPath := filepath.Join(t.TempDir(), "exported.json")
	if err := os.WriteFile(exportPath, []byte(exported), 0o600); err != nil {
		t.Fatalf("write export: %v", err)
	}
	var imported struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(runWorkflowSubcmd(t, cfg, "import", "--file", exportPath)), &imported); err != nil {
		t.Fatalf("parse import output: %v", err)
	}

	// Assert identical nodes via store queries.
	db2, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db2.Close()

	type nodeRow struct {
		id, typ string
		x, y    float64
	}
	got := map[string]nodeRow{}
	rows, err := db2.DB.Query(`SELECT id, node_type, position_x, position_y FROM workflow_nodes WHERE workflow_id = ?`, imported.ID)
	if err != nil {
		t.Fatalf("query nodes: %v", err)
	}
	for rows.Next() {
		var r nodeRow
		if err := rows.Scan(&r.id, &r.typ, &r.x, &r.y); err != nil {
			t.Fatalf("scan node: %v", err)
		}
		got[r.id] = r
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate nodes: %v", err)
	}
	rows.Close()

	wantNodes := map[string]nodeRow{
		n1: {n1, "trigger.manual", 0, 0},
		n2: {n2, "core.set", 100, 200},
		n3: {n3, "core.set", 300, 400},
	}
	if len(got) != len(wantNodes) {
		t.Fatalf("expected %d nodes after import, got %d (%v)", len(wantNodes), len(got), got)
	}
	for id, want := range wantNodes {
		if got[id] != want {
			t.Fatalf("node %q: got %+v, want %+v", id, got[id], want)
		}
	}

	// Assert identical connections — both the endpoint set and that every
	// edge's FKs resolve to nodes of this workflow (the join that used to
	// find zero rows when legacy imports produced empty endpoints).
	edges := map[string]string{}
	rows, err = db2.DB.Query(`SELECT c.source_node_id, c.target_node_id FROM workflow_connections c WHERE c.workflow_id = ?`, imported.ID)
	if err != nil {
		t.Fatalf("query connections: %v", err)
	}
	for rows.Next() {
		var s, g string
		if err := rows.Scan(&s, &g); err != nil {
			t.Fatalf("scan connection: %v", err)
		}
		edges[s] = g
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate connections: %v", err)
	}
	rows.Close()

	wantEdges := map[string]string{n1: n2, n2: n3}
	if len(edges) != len(wantEdges) {
		t.Fatalf("expected %d connections after import, got %d (%v)", len(wantEdges), len(edges), edges)
	}
	for s, wantTarget := range wantEdges {
		if edges[s] != wantTarget {
			t.Fatalf("edge from %s: got target %q, want %q", s, edges[s], wantTarget)
		}
	}

	var fkEdges int
	if err := db2.DB.QueryRow(`SELECT COUNT(*) FROM workflow_connections c
		JOIN workflow_nodes s ON s.id = c.source_node_id AND s.workflow_id = c.workflow_id
		JOIN workflow_nodes g ON g.id = c.target_node_id AND g.workflow_id = c.workflow_id
		WHERE c.workflow_id = ?`, imported.ID).Scan(&fkEdges); err != nil {
		t.Fatalf("count FK-resolvable edges: %v", err)
	}
	if fkEdges != 2 {
		t.Fatalf("expected 2 FK-resolvable edges after import, got %d", fkEdges)
	}
}

// TestWorkflowImportLegacyFormat guards RV6-3's import direction: legacy
// exports (node_type/position_x/source_node_id keys) must import with the
// correct node types, positions, and edges instead of silently producing
// empty node types and FK-dangling connections.
func TestWorkflowImportLegacyFormat(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dbPath := filepath.Join(t.TempDir(), "legacy.db")
	cfg := &globalConfig{DBPath: dbPath, JSONOutput: true, ProfileID: "default"}

	path := filepath.Join(t.TempDir(), "legacy.json")
	if err := os.WriteFile(path, []byte(legacyWorkflowFile), 0o600); err != nil {
		t.Fatalf("write legacy file: %v", err)
	}
	var imported struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(runWorkflowSubcmd(t, cfg, "import", "--file", path)), &imported); err != nil {
		t.Fatalf("parse import output: %v", err)
	}

	db, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	gotTypes := map[string]string{}
	gotPos := map[string][2]float64{}
	rows, err := db.DB.Query(`SELECT id, node_type, position_x, position_y FROM workflow_nodes WHERE workflow_id = ?`, imported.ID)
	if err != nil {
		t.Fatalf("query nodes: %v", err)
	}
	for rows.Next() {
		var id, typ string
		var x, y float64
		if err := rows.Scan(&id, &typ, &x, &y); err != nil {
			t.Fatalf("scan node: %v", err)
		}
		gotTypes[id] = typ
		gotPos[id] = [2]float64{x, y}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate nodes: %v", err)
	}
	rows.Close()

	if gotTypes["trigger"] != "trigger.manual" || gotTypes["set"] != "core.set" {
		t.Fatalf("legacy node types not converted, got %v", gotTypes)
	}
	if gotPos["trigger"] != [2]float64{5, 7} || gotPos["set"] != [2]float64{9, 11} {
		t.Fatalf("legacy positions not converted, got %v", gotPos)
	}

	var src, dst string
	if err := db.DB.QueryRow(`SELECT c.source_node_id, c.target_node_id FROM workflow_connections c WHERE c.workflow_id = ?`, imported.ID).Scan(&src, &dst); err != nil {
		t.Fatalf("query connection: %v", err)
	}
	if src != "trigger" || dst != "set" {
		t.Fatalf("legacy edge not converted, got %s → %s", src, dst)
	}

	var fkEdges int
	if err := db.DB.QueryRow(`SELECT COUNT(*) FROM workflow_connections c
		JOIN workflow_nodes s ON s.id = c.source_node_id AND s.workflow_id = c.workflow_id
		JOIN workflow_nodes g ON g.id = c.target_node_id AND g.workflow_id = c.workflow_id
		WHERE c.workflow_id = ?`, imported.ID).Scan(&fkEdges); err != nil {
		t.Fatalf("count FK-resolvable edges: %v", err)
	}
	if fkEdges != 1 {
		t.Fatalf("expected 1 FK-resolvable edge, got %d", fkEdges)
	}
}

// TestWorkflowImportRejectsEmptyNodeType: a node without any type key must
// be rejected with exit 3 and never persisted.
func TestWorkflowImportRejectsEmptyNodeType(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dbPath := filepath.Join(t.TempDir(), "empty-type.db")
	// The rejected import bails before initDB, so the DB must exist already
	// for the not-persisted assertion below to query it.
	seedDB, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatalf("seed db: %v", err)
	}
	if err := seedDB.ApplyMigrations(); err != nil {
		t.Fatalf("migrate db: %v", err)
	}
	if err := seedDB.Close(); err != nil {
		t.Fatalf("close seed db: %v", err)
	}
	cfg := &globalConfig{DBPath: dbPath, ProfileID: "default"}

	path := filepath.Join(t.TempDir(), "notype.json")
	if err := os.WriteFile(path, []byte(`{"name":"bad","nodes":[{"id":"n1","name":"N1","config":{}}],"connections":[]}`), 0o600); err != nil {
		t.Fatalf("write input: %v", err)
	}
	cmd := newWorkflowImportCmd(cfg)
	cmd.SetArgs([]string{"--file", path})
	err = cmd.Execute()
	if err == nil {
		t.Fatal("expected import of a typeless node to be rejected")
	}
	if code := exitCodeFor(err); code != 3 {
		t.Fatalf("expected exit code 3 for typeless node, got %d (%v)", code, err)
	}

	db, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	var n int
	if err := db.DB.QueryRow(`SELECT COUNT(*) FROM workflows WHERE name = 'bad'`).Scan(&n); err != nil {
		t.Fatalf("count workflows: %v", err)
	}
	if n != 0 {
		t.Fatalf("rejected workflow must not be persisted, found %d rows", n)
	}
}

// TestWorkflowImportFileErrorClassification guards RV1-6 for import: a
// missing --file is not-found (exit 2); any other IO error is a plain
// error (exit 1).
func TestWorkflowImportFileErrorClassification(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := &globalConfig{DBPath: filepath.Join(t.TempDir(), "io.db"), ProfileID: "default"}

	cmd := newWorkflowImportCmd(cfg)
	cmd.SetArgs([]string{"--file", filepath.Join(t.TempDir(), "missing.json")})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error for missing --file")
	} else if code := exitCodeFor(err); code != 2 {
		t.Fatalf("missing --file: exit %d, want 2 (%v)", code, err)
	}

	// Reading a directory is an IO error that is not not-exist.
	cmd2 := newWorkflowImportCmd(cfg)
	cmd2.SetArgs([]string{"--file", t.TempDir()})
	if err := cmd2.Execute(); err == nil {
		t.Fatal("expected error for --file pointing at a directory")
	} else if code := exitCodeFor(err); code != 1 {
		t.Fatalf("directory --file: exit %d, want 1 (%v)", code, err)
	}
}

// TestWorkflowDeleteNonexistentIsNotFound guards RV4-4: deleting an unknown
// workflow must exit 2 (and must not spin up the engine to find that out).
func TestWorkflowDeleteNonexistentIsNotFound(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := &globalConfig{DBPath: filepath.Join(t.TempDir(), "delete.db"), ProfileID: "default"}

	cmd := newWorkflowDeleteCmd(cfg)
	cmd.SetArgs([]string{"no-such-workflow", "--force"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for deleting a nonexistent workflow")
	}
	if code := exitCodeFor(err); code != 2 {
		t.Fatalf("expected exit code 2, got %d (%v)", code, err)
	}
}

// TestWorkflowRunNoWaitPersistsUnownedQueued guards the --no-wait fix
// (V3-F2): the command must persist the execution as an unowned QUEUED row
// (pid 0) for a live engine to adopt — NOT enqueue it into its own
// short-lived engine, where the run died cancelled the moment the CLI exited.
func TestWorkflowRunNoWaitPersistsUnownedQueued(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dbPath := filepath.Join(t.TempDir(), "nowait.db")
	cfg := &globalConfig{DBPath: dbPath, JSONOutput: true, ProfileID: "default"}

	path := writeTempWorkflow(t, validWorkflowFile)
	var imported struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(runWorkflowSubcmd(t, cfg, "import", "--file", path)), &imported); err != nil {
		t.Fatalf("parse import output: %v", err)
	}
	runWorkflowSubcmd(t, cfg, "activate", imported.ID)

	var noWait struct {
		ExecutionID string `json:"execution_id"`
		Status      string `json:"status"`
	}
	if err := json.Unmarshal([]byte(runWorkflowSubcmd(t, cfg, "run", imported.ID, "--no-wait")), &noWait); err != nil {
		t.Fatalf("parse run --no-wait output: %v", err)
	}
	if noWait.Status != "QUEUED" {
		t.Errorf("run --no-wait reported status %q, want QUEUED", noWait.Status)
	}

	db, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	var status string
	var pid int
	if err := db.DB.QueryRow(
		`SELECT status, COALESCE(pid, 0) FROM workflow_executions WHERE id = ?`, noWait.ExecutionID,
	).Scan(&status, &pid); err != nil {
		t.Fatalf("read execution row: %v", err)
	}
	if status != "QUEUED" {
		t.Errorf("persisted status = %q, want QUEUED (the CLI must not run it locally)", status)
	}
	if pid != 0 {
		t.Errorf("persisted pid = %d, want 0 (unowned row awaiting adoption)", pid)
	}
}

// TestNormalizeLegacyWorkflowJSON_Passthrough: JSON without legacy keys
// must pass through byte-identical (no re-marshal that could reorder keys).
func TestNormalizeLegacyWorkflowJSON_Passthrough(t *testing.T) {
	out, err := normalizeLegacyWorkflowJSON([]byte(validWorkflowFile))
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if string(out) != validWorkflowFile {
		t.Fatalf("native JSON must pass through unchanged, got: %q", out)
	}
}

// workflow_nodes.id is globally unique across workflows, so importing two
// workflows that share node ids (every bundled example uses "trigger") used
// to fail with "UNIQUE constraint failed: workflow_nodes.id". The second
// import must succeed with colliding ids deterministically remapped across
// nodes and connections, edges preserved.
func TestWorkflowImportNodeIDCollision(t *testing.T) {
	// Isolate the hybrid store's file half (~/.monoagent/workflows).
	t.Setenv("HOME", t.TempDir())

	dbPath := filepath.Join(t.TempDir(), "import.db")
	cfg := &globalConfig{DBPath: dbPath}

	importOne := func(name string) string {
		t.Helper()
		content := fmt.Sprintf(`{
  "name": %q,
  "nodes": [
    {"id": "trigger", "type": "trigger.manual", "name": "Manual", "position": {"x": 0, "y": 0}, "config": {}},
    {"id": "set", "type": "core.set", "name": "Set", "position": {"x": 1, "y": 0}, "config": {"fields": {}}}
  ],
  "connections": [
    {"id": "c1", "source": "trigger", "source_handle": "main", "target": "set", "target_handle": "main"}
  ]
}`, name)
		path := filepath.Join(t.TempDir(), "workflow.json")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write temp workflow: %v", err)
		}
		cmd := newWorkflowImportCmd(cfg)
		cmd.SetArgs([]string{"--file", path})
		return captureStdout(t, func() {
			if err := cmd.Execute(); err != nil {
				t.Fatalf("import %s: %v", name, err)
			}
		})
	}

	if out := importOne("collision-a"); strings.Contains(out, "Remapped") {
		t.Fatalf("first import should not remap any ids, got: %q", out)
	}
	out := importOne("collision-b")
	if !strings.Contains(out, "trigger → trigger-2") || !strings.Contains(out, "set → set-2") {
		t.Fatalf("second import should note remapped node ids, got: %q", out)
	}
	if !strings.Contains(out, "c1 → c1-2") {
		t.Fatalf("second import should note remapped connection ids, got: %q", out)
	}

	db, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	// Both workflows' nodes present, all ids distinct.
	var nodeCount, distinctIDs int
	if err := db.DB.QueryRow(`SELECT COUNT(*), COUNT(DISTINCT id) FROM workflow_nodes`).Scan(&nodeCount, &distinctIDs); err != nil {
		t.Fatalf("count nodes: %v", err)
	}
	if nodeCount != 4 || distinctIDs != 4 {
		t.Fatalf("expected 4 nodes with 4 distinct ids, got %d nodes / %d distinct", nodeCount, distinctIDs)
	}

	// The second workflow's nodes carry the remapped ids.
	var bNodes string
	if err := db.DB.QueryRow(`SELECT group_concat(id) FROM workflow_nodes
		WHERE workflow_id = (SELECT id FROM workflows WHERE name = 'collision-b')`).Scan(&bNodes); err != nil {
		t.Fatalf("load collision-b nodes: %v", err)
	}
	if !strings.Contains(bNodes, "trigger-2") || !strings.Contains(bNodes, "set-2") {
		t.Fatalf("collision-b nodes should be remapped, got: %q", bNodes)
	}

	// Edges preserved: each workflow has one edge whose endpoints are its own nodes.
	var edgeCount int
	if err := db.DB.QueryRow(`SELECT COUNT(*) FROM workflow_connections c
		JOIN workflow_nodes s ON s.id = c.source_node_id AND s.workflow_id = c.workflow_id
		JOIN workflow_nodes g ON g.id = c.target_node_id AND g.workflow_id = c.workflow_id`).Scan(&edgeCount); err != nil {
		t.Fatalf("count preserved edges: %v", err)
	}
	if edgeCount != 2 {
		t.Fatalf("expected 2 preserved edges (one per workflow), got %d", edgeCount)
	}
}
