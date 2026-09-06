package main

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/storage"
	"github.com/monoes/mono-agent/internal/workflow"
)

// newTestApp builds an App wired to a temp SQLite DB and temp workflow-file
// directory — the same hybrid store startup() constructs, without the Wails
// runtime. The active profile defaults to "default".
func newTestApp(t *testing.T) *App {
	t.Helper()
	sdb, err := storage.NewDatabase(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewDatabase: %v", err)
	}
	if err := sdb.ApplyMigrations(); err != nil {
		t.Fatalf("ApplyMigrations: %v", err)
	}
	t.Cleanup(func() { sdb.DB.Close() })

	fileStore, err := workflow.NewWorkflowFileStore(filepath.Join(t.TempDir(), "workflows"))
	if err != nil {
		t.Fatalf("NewWorkflowFileStore: %v", err)
	}
	return &App{
		db:          sdb.DB,
		wfStore:     workflow.NewHybridWorkflowStore(fileStore, workflow.NewSQLiteWorkflowStore(sdb.DB)),
		runningCmds: make(map[string]*exec.Cmd),
	}
}

// TestSaveWorkflow_ListReturnsItBeforeAnyRun covers RA1-2/RA1-3: a workflow
// saved through the GUI path (file-first) must appear in ListWorkflows
// immediately, and a SQL metadata row tagged with the active profile must
// exist for it.
func TestSaveWorkflow_ListReturnsItBeforeAnyRun(t *testing.T) {
	a := newTestApp(t)

	saved, err := a.SaveWorkflow(SaveWorkflowRequest{Name: "gui-saved", Description: "d", IsActive: true})
	if err != nil {
		t.Fatalf("SaveWorkflow: %v", err)
	}

	list, err := a.ListWorkflows()
	if err != nil {
		t.Fatalf("ListWorkflows: %v", err)
	}
	found := false
	for _, s := range list {
		if s.ID == saved.ID {
			found = true
			if s.Name != "gui-saved" || !s.IsActive {
				t.Fatalf("unexpected summary for saved workflow: %+v", s)
			}
		}
	}
	if !found {
		t.Fatalf("saved workflow %s missing from ListWorkflows output: %+v", saved.ID, list)
	}

	var profileID string
	if err := a.db.QueryRow(`SELECT profile_id FROM workflows WHERE id = ?`, saved.ID).Scan(&profileID); err != nil {
		t.Fatalf("expected SQL metadata row for %s: %v", saved.ID, err)
	}
	if profileID != "default" {
		t.Fatalf("expected SQL row tagged profile 'default', got %q", profileID)
	}
}

// TestListWorkflows_IncludesFileOnlyWorkflow covers the CLI-created case of
// RA1-2: a workflow that exists only as a JSON file (no SQL row at all, e.g.
// saved by `monoagentcli workflow create` and never run) must still be listed.
func TestListWorkflows_IncludesFileOnlyWorkflow(t *testing.T) {
	a := newTestApp(t)

	// Write through the hybrid store directly — file-first, no SQL metadata
	// row — the exact state RA1-2 described.
	if err := a.wfStore.SaveWorkflow(t.Context(), &workflow.Workflow{
		ID: "file-only-1", Name: "cli-created", IsActive: true, ProfileID: "default",
	}); err != nil {
		t.Fatalf("seeding file-only workflow: %v", err)
	}

	list, err := a.ListWorkflows()
	if err != nil {
		t.Fatalf("ListWorkflows: %v", err)
	}
	found := false
	for _, s := range list {
		if s.ID == "file-only-1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("file-only workflow missing from ListWorkflows output: %+v", list)
	}
}

// TestListWorkflows_FiltersByProfile: workflows saved under one profile must
// not leak into another profile's list.
func TestListWorkflows_FiltersByProfile(t *testing.T) {
	a := newTestApp(t)

	if _, err := a.SaveWorkflow(SaveWorkflowRequest{Name: "mine", IsActive: true}); err != nil {
		t.Fatalf("SaveWorkflow: %v", err)
	}

	a.setActiveProfileID("other-profile")
	list, err := a.ListWorkflows()
	if err != nil {
		t.Fatalf("ListWorkflows: %v", err)
	}
	for _, s := range list {
		if s.Name == "mine" {
			t.Fatalf("workflow from profile 'default' leaked into 'other-profile' list: %+v", list)
		}
	}
}

// TestRunWorkflow_RejectsInactiveWorkflow covers RA1-7: running an inactive
// workflow must return an error instead of silently flipping is_active.
func TestRunWorkflow_RejectsInactiveWorkflow(t *testing.T) {
	a := newTestApp(t)

	saved, err := a.SaveWorkflow(SaveWorkflowRequest{Name: "dormant", IsActive: false})
	if err != nil {
		t.Fatalf("SaveWorkflow: %v", err)
	}

	err = a.RunWorkflow(saved.ID)
	if err == nil {
		t.Fatal("expected error running an inactive workflow, got nil")
	}
	if !strings.Contains(err.Error(), "inactive") {
		t.Fatalf("expected error to mention inactivity, got: %v", err)
	}
}

// TestRunWorkflow_RefusesConcurrentRun covers RA1-11: a second RunWorkflow
// call for a workflow with a live registered subprocess must fail clearly
// instead of overwriting the registry entry (orphaning the first process).
func TestRunWorkflow_RefusesConcurrentRun(t *testing.T) {
	a := newTestApp(t)

	saved, err := a.SaveWorkflow(SaveWorkflowRequest{Name: "busy", IsActive: true})
	if err != nil {
		t.Fatalf("SaveWorkflow: %v", err)
	}

	// Simulate an in-flight run without spawning anything.
	a.runningCmds[saved.ID] = &exec.Cmd{}

	err = a.RunWorkflow(saved.ID)
	if err == nil {
		t.Fatal("expected error for a workflow that is already running, got nil")
	}
	if !strings.Contains(err.Error(), "already running") {
		t.Fatalf("expected error to mention an in-flight run, got: %v", err)
	}
}

// TestGetExecutionDetail_RedactsSecretsInItems is a regression test for the
// HIGH security finding at app_workflows.go:687 — GetExecutionDetail used to
// put the raw input_items/output_items column strings straight into its
// return value with no redaction, unlike every other execution-output
// surface (cmd/monoagentcli/execution_json.go, internal/mcp/tools.go,
// internal/httpapi/server.go), all of which redact credential-shaped keys
// first. A node like vault.secret_get intentionally writes decrypted
// secrets into the item stream relying on display-boundary masking, so an
// unredacted GetExecutionDetail would leak them straight into the desktop
// app's execution-history view.
func TestGetExecutionDetail_RedactsSecretsInItems(t *testing.T) {
	a := newTestApp(t)

	saved, err := a.SaveWorkflow(SaveWorkflowRequest{Name: "secret-flow", IsActive: true})
	if err != nil {
		t.Fatalf("SaveWorkflow: %v", err)
	}

	const executionID = "exec-1"
	if _, err := a.db.Exec(
		`INSERT INTO workflow_executions (id, workflow_id, status, profile_id) VALUES (?, ?, 'SUCCESS', 'default')`,
		executionID, saved.ID,
	); err != nil {
		t.Fatalf("inserting workflow_executions row: %v", err)
	}

	outputItems, err := json.Marshal([]workflow.Item{
		{JSON: map[string]interface{}{
			"api_key": "sk-live-super-secret-value",
			"label":   "not secret",
		}},
	})
	if err != nil {
		t.Fatalf("marshalling output items: %v", err)
	}

	if _, err := a.db.Exec(
		`INSERT INTO workflow_execution_nodes (id, execution_id, node_id, node_name, status, input_items, output_items)
		 VALUES (?, ?, 'n1', 'Get Secret', 'SUCCESS', '[]', ?)`,
		"node-1", executionID, string(outputItems),
	); err != nil {
		t.Fatalf("inserting workflow_execution_nodes row: %v", err)
	}

	detail, err := a.GetExecutionDetail(executionID)
	if err != nil {
		t.Fatalf("GetExecutionDetail: %v", err)
	}

	nodes, ok := detail["nodes"].([]map[string]interface{})
	if !ok || len(nodes) != 1 {
		t.Fatalf("expected exactly one node in execution detail, got: %+v", detail["nodes"])
	}

	rawOutput, _ := nodes[0]["output_items"].(string)
	if strings.Contains(rawOutput, "sk-live-super-secret-value") {
		t.Fatalf("expected secret to be redacted from output_items, got raw value: %s", rawOutput)
	}
	if !strings.Contains(rawOutput, "***") {
		t.Fatalf("expected redaction marker in output_items, got: %s", rawOutput)
	}
	if !strings.Contains(rawOutput, "not secret") {
		t.Fatalf("expected non-secret value to survive redaction, got: %s", rawOutput)
	}
}
