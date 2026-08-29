package workflow

import (
	"context"
	"testing"
)

func TestGetExecutionStatus(t *testing.T) {
	s := newExecStore(t)
	insertExec(t, s, "e1", "RUNNING", "")

	status, err := s.GetExecutionStatus(context.Background(), "e1")
	if err != nil {
		t.Fatalf("GetExecutionStatus: %v", err)
	}
	if status != "RUNNING" {
		t.Errorf("status = %q, want RUNNING", status)
	}

	if err := s.UpdateExecutionStatus(context.Background(), "e1", "SUCCESS", ""); err != nil {
		t.Fatalf("UpdateExecutionStatus: %v", err)
	}
	status, err = s.GetExecutionStatus(context.Background(), "e1")
	if err != nil {
		t.Fatalf("GetExecutionStatus after update: %v", err)
	}
	if status != "SUCCESS" {
		t.Errorf("status after update = %q, want SUCCESS", status)
	}
}

func TestGetExecutionStatusMissing(t *testing.T) {
	s := newExecStore(t)
	status, err := s.GetExecutionStatus(context.Background(), "no-such-exec")
	if err != nil {
		t.Fatalf("missing execution must not error, got: %v", err)
	}
	if status != "" {
		t.Errorf("missing execution status = %q, want \"\"", status)
	}
}
