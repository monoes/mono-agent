package workflow

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestWorkflowFileStore_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	store, err := NewWorkflowFileStore(dir)
	if err != nil {
		t.Fatalf("NewWorkflowFileStore: %v", err)
	}
	ctx := context.Background()

	wf := &Workflow{
		Name:      "Test Workflow",
		IsActive:  true,
		CreatedAt: time.Now().UTC(),
		Nodes: []WorkflowNode{
			{
				ID:     "node-1",
				Type:   "trigger.manual",
				Name:   "Start",
				Schema: &NodeSchema{Fields: []NodeSchemaField{}},
				Config: map[string]interface{}{},
			},
		},
		Connections: []WorkflowConnection{},
	}

	if err := store.SaveWorkflow(ctx, wf); err != nil {
		t.Fatalf("SaveWorkflow: %v", err)
	}
	if wf.ID == "" {
		t.Fatal("expected ID to be assigned")
	}

	loaded, err := store.GetWorkflow(ctx, wf.ID)
	if err != nil {
		t.Fatalf("GetWorkflow: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected non-nil workflow")
	}
	if loaded.Name != "Test Workflow" {
		t.Errorf("name mismatch: got %q", loaded.Name)
	}
	if len(loaded.Nodes) != 1 {
		t.Errorf("expected 1 node, got %d", len(loaded.Nodes))
	}

	list, err := store.ListWorkflows(ctx)
	if err != nil {
		t.Fatalf("ListWorkflows: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1 workflow in list, got %d", len(list))
	}

	if err := store.DeleteWorkflow(ctx, wf.ID); err != nil {
		t.Fatalf("DeleteWorkflow: %v", err)
	}
	_, statErr := os.Stat(store.filePath(wf.ID))
	if !os.IsNotExist(statErr) {
		t.Fatal("expected file to be deleted")
	}
}

func TestWorkflowFileStore_GetNotFound(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewWorkflowFileStore(dir)
	wf, err := store.GetWorkflow(context.Background(), "nonexistent-id")
	if err != nil {
		t.Fatalf("expected nil error for not found, got: %v", err)
	}
	if wf != nil {
		t.Fatal("expected nil workflow for not found")
	}
}

func TestWorkflowFileStore_SchemaEmbedded(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewWorkflowFileStore(dir)
	ctx := context.Background()

	// Node with no Schema — should get default schema auto-embedded on save
	wf := &Workflow{
		Name: "Schema Test",
		Nodes: []WorkflowNode{
			{ID: "n1", Type: "service.google_sheets", Name: "GSheets", Config: map[string]interface{}{}},
		},
	}
	if err := store.SaveWorkflow(ctx, wf); err != nil {
		t.Fatalf("SaveWorkflow: %v", err)
	}

	loaded, _ := store.GetWorkflow(ctx, wf.ID)
	if loaded == nil || len(loaded.Nodes) == 0 {
		t.Fatal("expected nodes")
	}
	if loaded.Nodes[0].Schema == nil {
		t.Fatal("expected schema to be auto-embedded on save")
	}
	if len(loaded.Nodes[0].Schema.Fields) == 0 {
		t.Fatal("expected non-empty fields for service.google_sheets")
	}
}
