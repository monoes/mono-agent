package workflow

import (
	"context"
	"database/sql"
	"fmt"
)

// GetExecutionStatus returns only the status string of an execution — a
// single-column query meant for polling loops, so waiters avoid the full
// GetExecution load (trigger data + per-node output JSON parsing) on every
// tick. Returns "" (not an error) when the execution does not exist,
// mirroring GetExecution's nil, nil convention.
func (s *SQLiteWorkflowStore) GetExecutionStatus(ctx context.Context, id string) (string, error) {
	var status string
	err := s.db.QueryRowContext(ctx,
		`SELECT status FROM workflow_executions WHERE id = ?`, id,
	).Scan(&status)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("getting execution status %s: %w", id, err)
	}
	return status, nil
}

// GetExecutionStatus delegates execution-status polling to the SQLite store.
func (h *HybridWorkflowStore) GetExecutionStatus(ctx context.Context, id string) (string, error) {
	return h.sql.GetExecutionStatus(ctx, id)
}

// GetExecutionStatus returns just the status of an execution (polling helper).
func (e *WorkflowEngine) GetExecutionStatus(ctx context.Context, id string) (string, error) {
	return e.store.GetExecutionStatus(ctx, id)
}
