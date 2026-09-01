package workflow

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// benchLargeWorkflow constructs a workflow with ~100 nodes and ~200 edges
// (each node after the first connects back to the previous two, roughly
// doubling edges relative to nodes) so save/load benchmarks reflect a
// workflow larger than the small fixtures used elsewhere in this package.
func benchLargeWorkflow(name string) *Workflow {
	const nodeCount = 100
	nodes := make([]WorkflowNode, nodeCount)
	for i := 0; i < nodeCount; i++ {
		nodes[i] = WorkflowNode{
			ID:     fmt.Sprintf("n%d", i),
			Type:   "core.set",
			Name:   fmt.Sprintf("Node %d", i),
			Config: map[string]interface{}{"assignments": []interface{}{}},
		}
	}

	var conns []WorkflowConnection
	for i := 1; i < nodeCount; i++ {
		conns = append(conns, WorkflowConnection{
			ID:           fmt.Sprintf("c%d-main", i),
			SourceNodeID: fmt.Sprintf("n%d", i-1),
			SourceHandle: "main",
			TargetNodeID: fmt.Sprintf("n%d", i),
			TargetHandle: "main",
		})
		if i >= 2 {
			conns = append(conns, WorkflowConnection{
				ID:           fmt.Sprintf("c%d-extra", i),
				SourceNodeID: fmt.Sprintf("n%d", i-2),
				SourceHandle: "main",
				TargetNodeID: fmt.Sprintf("n%d", i),
				TargetHandle: "secondary",
			})
		}
	}

	return &Workflow{
		Name:        name,
		CreatedAt:   time.Now().UTC(),
		Nodes:       nodes,
		Connections: conns,
	}
}

// BenchmarkWorkflowFileStore_SaveLargeWorkflow measures saving a workflow
// with ~100 nodes and ~200 edges to the JSON file store.
func BenchmarkWorkflowFileStore_SaveLargeWorkflow(b *testing.B) {
	dir := b.TempDir()
	store, err := NewWorkflowFileStore(dir)
	if err != nil {
		b.Fatalf("NewWorkflowFileStore: %v", err)
	}
	ctx := context.Background()
	wf := benchLargeWorkflow("bench-save")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		wf.ID = "" // force a fresh ID/insert path each iteration
		if err := store.SaveWorkflow(ctx, wf); err != nil {
			b.Fatalf("SaveWorkflow: %v", err)
		}
	}
}

// BenchmarkWorkflowFileStore_LoadLargeWorkflow measures loading a workflow
// with ~100 nodes and ~200 edges back from the JSON file store.
func BenchmarkWorkflowFileStore_LoadLargeWorkflow(b *testing.B) {
	dir := b.TempDir()
	store, err := NewWorkflowFileStore(dir)
	if err != nil {
		b.Fatalf("NewWorkflowFileStore: %v", err)
	}
	ctx := context.Background()
	wf := benchLargeWorkflow("bench-load")
	if err := store.SaveWorkflow(ctx, wf); err != nil {
		b.Fatalf("SaveWorkflow: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := store.GetWorkflow(ctx, wf.ID); err != nil {
			b.Fatalf("GetWorkflow: %v", err)
		}
	}
}
