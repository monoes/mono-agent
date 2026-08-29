package workflow

import (
	"context"
	"testing"

	"github.com/rs/zerolog"
)

// TestCheckWorkflowProfile is a regression test: engine methods that load a
// workflow by bare ID (SaveWorkflow, ActivateWorkflow, DeactivateWorkflow,
// TriggerWorkflow, RetryExecution, GetWorkflow) must reject workflows that
// belong to a different profile than the engine's active one.
func TestCheckWorkflowProfile(t *testing.T) {
	e := &WorkflowEngine{profileID: "profile-a"}

	cases := []struct {
		name    string
		wf      *Workflow
		wantErr bool
	}{
		{"same profile", &Workflow{ID: "wf-1", ProfileID: "profile-a"}, false},
		{"different profile", &Workflow{ID: "wf-2", ProfileID: "profile-b"}, true},
		{"unset profile (legacy row)", &Workflow{ID: "wf-3", ProfileID: ""}, false},
	}

	for _, tc := range cases {
		err := e.checkWorkflowProfile(tc.wf)
		if tc.wantErr && err == nil {
			t.Errorf("%s: expected error, got nil", tc.name)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("%s: unexpected error: %v", tc.name, err)
		}
	}
}

// TestExecutionProfileID is a regression test for a bug where every new
// WorkflowExecution silently fell back to the profile_id column's SQL
// default ('default') because nothing ever set it — breaking
// GetExecutionDetail's profile-scoped lookup for any workflow not owned by
// the 'default' profile, and specifically for a long-running daemon's single
// engine instance (RestoreActiveWorkflows) handling many profiles' scheduled
// triggers at once, where the engine's own profileID is not the right value
// for most of those workflows.
func TestExecutionProfileID(t *testing.T) {
	cases := []struct {
		name            string
		wf              *Workflow
		engineProfileID string
		want            string
	}{
		{"workflow has its own profile", &Workflow{ID: "wf-1", ProfileID: "halansari"}, "default", "halansari"},
		{"workflow profile differs from engine's", &Workflow{ID: "wf-2", ProfileID: "monoes"}, "halansari", "monoes"},
		{"legacy workflow with no profile falls back to engine's", &Workflow{ID: "wf-3", ProfileID: ""}, "default", "default"},
		{"nil workflow falls back to engine's", nil, "edge", "edge"},
	}
	for _, tc := range cases {
		if got := executionProfileID(tc.wf, tc.engineProfileID); got != tc.want {
			t.Errorf("%s: executionProfileID() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestTriggerWorkflow_StampsWorkflowsOwnProfile is an integration-level
// regression test proving TriggerWorkflow actually persists the workflow's
// own ProfileID on the created execution (not the engine's), via the real
// CreateExecution call path.
func TestTriggerWorkflow_StampsWorkflowsOwnProfile(t *testing.T) {
	store := &stubStore{
		workflowToReturn: &Workflow{ID: "wf-halansari", ProfileID: "halansari", IsActive: true},
	}
	e := &WorkflowEngine{
		profileID:        "default", // the daemon's own engine profile — must NOT end up on the exec
		allowAllProfiles: true,
		store:            store,
		queue:            NewExecutionQueue(1, 1, func(context.Context, ExecutionRequest) {}, zerolog.Nop()),
		logger:           zerolog.Nop(),
	}
	execID, err := e.TriggerWorkflow(context.Background(), "wf-halansari", nil)
	if err != nil {
		t.Fatalf("TriggerWorkflow: %v", err)
	}
	if len(store.createdExecs) != 1 {
		t.Fatalf("expected 1 created execution, got %d", len(store.createdExecs))
	}
	got := store.createdExecs[0]
	if got.ID != execID {
		t.Errorf("returned execID %q doesn't match created execution %q", execID, got.ID)
	}
	if got.ProfileID != "halansari" {
		t.Errorf("execution ProfileID = %q, want %q (the workflow's own profile, not the engine's %q)", got.ProfileID, "halansari", e.profileID)
	}
}
