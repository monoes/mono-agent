package workflow

import (
	"context"
	"errors"
	"testing"

	"github.com/rs/zerolog"
)

// getWorkflowErrorStore makes GetWorkflow fail while recording every other
// call through to the embedded stubStore.
type getWorkflowErrorStore struct {
	*stubStore
	getErr error
}

func (s *getWorkflowErrorStore) GetWorkflow(context.Context, string) (*Workflow, error) {
	return nil, s.getErr
}

func newTriggerEngine(store WorkflowStore) *WorkflowEngine {
	return NewWorkflowEngineWithStore(store, nil, nil, NewNodeTypeRegistry(),
		EngineConfig{ProfileID: "engine-profile"}, zerolog.Nop())
}

// TestHandleTrigger_GetWorkflowErrorSkips is the F3-4 guard: a failed
// workflow load must NOT create an execution (previously it created one
// stamped with the engine's profile).
func TestHandleTrigger_GetWorkflowErrorSkips(t *testing.T) {
	store := &getWorkflowErrorStore{stubStore: &stubStore{}, getErr: errors.New("store exploded")}
	eng := newTriggerEngine(store)

	eng.handleTrigger("wf-x", "t1", nil)

	if n := len(store.createdExecs); n != 0 {
		t.Fatalf("created %d executions after GetWorkflow error, want 0", n)
	}
}

// TestHandleTrigger_NilWorkflowSkips: a missing workflow (nil, no error)
// must likewise not create an execution.
func TestHandleTrigger_NilWorkflowSkips(t *testing.T) {
	store := &stubStore{} // workflowToReturn == nil
	eng := newTriggerEngine(store)

	eng.handleTrigger("wf-x", "t1", nil)

	if n := len(store.createdExecs); n != 0 {
		t.Fatalf("created %d executions for nil workflow, want 0", n)
	}
}

// TestHandleTrigger_ActiveWorkflowStampsWorkflowProfile pins the behaviour
// the error path must not disturb: a loadable active workflow still gets an
// execution stamped with the workflow's own profile, not the engine's.
func TestHandleTrigger_ActiveWorkflowStampsWorkflowProfile(t *testing.T) {
	store := &stubStore{workflowToReturn: &Workflow{
		ID: "wf-x", IsActive: true, ProfileID: "wf-profile",
		Nodes: []WorkflowNode{{ID: "t1", Type: "trigger.schedule"}},
	}}
	eng := newTriggerEngine(store)

	eng.handleTrigger("wf-x", "t1", nil)

	if n := len(store.createdExecs); n != 1 {
		t.Fatalf("created %d executions, want 1", n)
	}
	exec := store.createdExecs[0]
	if exec.ProfileID != "wf-profile" {
		t.Errorf("execution ProfileID = %q, want workflow's own %q", exec.ProfileID, "wf-profile")
	}
	if exec.TriggerType != "trigger.schedule" {
		t.Errorf("execution TriggerType = %q, want trigger.schedule", exec.TriggerType)
	}
}
