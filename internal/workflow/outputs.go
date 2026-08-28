package workflow

import (
	"context"
)

// GetExecutionNodeOutputs returns the per-node run records of an execution —
// including each node's persisted output items — ordered by creation (the
// order the engine ran them in). It reads the workflow_execution_nodes table
// directly so callers (e.g. `workflow run --json`) can expose node outputs
// without re-loading the whole execution graph.
func (s *SQLiteWorkflowStore) GetExecutionNodeOutputs(ctx context.Context, executionID string) ([]WorkflowExecutionNode, error) {
	return s.loadExecutionNodes(ctx, executionID)
}

// GetExecutionNodeOutputs is the HybridWorkflowStore delegation of
// SQLiteWorkflowStore.GetExecutionNodeOutputs.
func (h *HybridWorkflowStore) GetExecutionNodeOutputs(ctx context.Context, executionID string) ([]WorkflowExecutionNode, error) {
	return h.sql.GetExecutionNodeOutputs(ctx, executionID)
}
