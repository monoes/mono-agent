package workflow

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestWorkflowFileStore_ListCachesUnchangedFiles: consecutive ListWorkflows
// calls with unchanged files must reuse the cached parse (observable via
// pointer identity — a re-parse allocates a fresh *Workflow), and a changed
// file (size/mtime), SaveWorkflow, and DeleteWorkflow must each invalidate.
func TestWorkflowFileStore_ListCachesUnchangedFiles(t *testing.T) {
	dir := t.TempDir()
	store, err := NewWorkflowFileStore(dir)
	if err != nil {
		t.Fatalf("NewWorkflowFileStore: %v", err)
	}
	ctx := context.Background()

	wf1 := &Workflow{Name: "one", CreatedAt: time.Now().UTC(), Nodes: []WorkflowNode{}}
	wf2 := &Workflow{Name: "two", CreatedAt: time.Now().UTC(), Nodes: []WorkflowNode{}}
	for _, wf := range []*Workflow{wf1, wf2} {
		if err := store.SaveWorkflow(ctx, wf); err != nil {
			t.Fatalf("SaveWorkflow: %v", err)
		}
	}

	first := listByID(t, store, ctx)
	if len(first) != 2 {
		t.Fatalf("expected 2 workflows, got %d", len(first))
	}

	second := listByID(t, store, ctx)
	for id, wf := range first {
		if second[id] != wf {
			t.Fatalf("workflow %q was re-parsed on unchanged files (cache miss)", id)
		}
	}

	// External rewrite with a different size invalidates via stat check.
	path := filepath.Join(dir, wf1.ID+".json")
	if err := os.WriteFile(path, []byte(`{"id":"`+wf1.ID+`","name":"one-external","updated_at":"2026-01-02T03:04:05Z"}`), 0o644); err != nil {
		t.Fatalf("rewrite file: %v", err)
	}
	third := listByID(t, store, ctx)
	if third[wf1.ID] == first[wf1.ID] {
		t.Fatal("externally rewritten file still served from cache")
	}
	if third[wf1.ID].Name != "one-external" {
		t.Fatalf("externally rewritten file not re-parsed, name=%q", third[wf1.ID].Name)
	}
	if third[wf2.ID] != first[wf2.ID] {
		t.Fatal("unrelated workflow was evicted from cache")
	}

	// SaveWorkflow invalidates its own entry.
	wf2.Name = "two-updated"
	if err := store.SaveWorkflow(ctx, wf2); err != nil {
		t.Fatalf("SaveWorkflow update: %v", err)
	}
	fourth := listByID(t, store, ctx)
	if fourth[wf2.ID].Name != "two-updated" {
		t.Fatalf("SaveWorkflow did not invalidate cache, name=%q", fourth[wf2.ID].Name)
	}

	// DeleteWorkflow removes the workflow from subsequent lists.
	if err := store.DeleteWorkflow(ctx, wf1.ID); err != nil {
		t.Fatalf("DeleteWorkflow: %v", err)
	}
	fifth := listByID(t, store, ctx)
	if _, ok := fifth[wf1.ID]; ok {
		t.Fatal("deleted workflow still listed")
	}
	if len(fifth) != 1 {
		t.Fatalf("expected 1 workflow after delete, got %d", len(fifth))
	}
}

func listByID(t *testing.T, store *WorkflowFileStore, ctx context.Context) map[string]*Workflow {
	t.Helper()
	list, err := store.ListWorkflows(ctx)
	if err != nil {
		t.Fatalf("ListWorkflows: %v", err)
	}
	out := make(map[string]*Workflow, len(list))
	for _, wf := range list {
		out[wf.ID] = wf
	}
	return out
}

// BenchmarkWorkflowFileStore_List compares repeated listing with and without
// the parse cache (flush it to measure the uncached path). The cache makes
// the second and later listings skip JSON parsing entirely.
func BenchmarkWorkflowFileStore_List(b *testing.B) {
	dir := b.TempDir()
	store, err := NewWorkflowFileStore(dir)
	if err != nil {
		b.Fatalf("NewWorkflowFileStore: %v", err)
	}
	ctx := context.Background()
	for i := 0; i < 50; i++ {
		wf := &Workflow{
			Name:      "wf",
			CreatedAt: time.Now().UTC(),
			Nodes:     []WorkflowNode{{ID: "n", Type: "core.set", Name: "N", Config: map[string]interface{}{}}},
		}
		if err := store.SaveWorkflow(ctx, wf); err != nil {
			b.Fatalf("SaveWorkflow: %v", err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := store.ListWorkflows(ctx); err != nil {
			b.Fatalf("ListWorkflows: %v", err)
		}
	}
}

func BenchmarkWorkflowFileStore_ListNoCache(b *testing.B) {
	dir := b.TempDir()
	store, err := NewWorkflowFileStore(dir)
	if err != nil {
		b.Fatalf("NewWorkflowFileStore: %v", err)
	}
	ctx := context.Background()
	for i := 0; i < 50; i++ {
		wf := &Workflow{
			Name:      "wf",
			CreatedAt: time.Now().UTC(),
			Nodes:     []WorkflowNode{{ID: "n", Type: "core.set", Name: "N", Config: map[string]interface{}{}}},
		}
		if err := store.SaveWorkflow(ctx, wf); err != nil {
			b.Fatalf("SaveWorkflow: %v", err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		store.mu.Lock()
		store.cache = make(map[string]fileCacheEntry)
		store.mu.Unlock()
		if _, err := store.ListWorkflows(ctx); err != nil {
			b.Fatalf("ListWorkflows: %v", err)
		}
	}
}
