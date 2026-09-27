package workflow

import (
	"context"
	"testing"
	"time"
)

// Executions written without an error_message (the actions migration does
// that) must still list and load: the column is nullable.
func TestExecutionsWithNullErrorMessage(t *testing.T) {
	s := newExecStore(t)
	insertExecAt(t, s, "migrated", "SUCCESS", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	if _, err := s.db.Exec(`UPDATE workflow_executions SET trigger_type = 'manual', trigger_data = '{}', error_message = NULL WHERE id = 'migrated'`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	list, err := s.ListExecutions(ctx, "wf", 0)
	if err != nil || len(list) != 1 || list[0].ErrorMessage != "" {
		t.Fatalf("ListExecutions = %+v, %v", list, err)
	}
}
