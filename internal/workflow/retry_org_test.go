package workflow

import (
	"context"
	"errors"
	"testing"

	"github.com/rs/zerolog"
)

// A run an org role started through a grant or an automation-role message
// is not retried: the retry would run the automation again with no ledger
// row and outside the grant's caps (#127). Other runs still reach the
// usual retry path.
func TestRetryExecutionRefusesOrgStartedRuns(t *testing.T) {
	store := newFullEngineStore(t)
	ctx := context.Background()
	e := &WorkflowEngine{
		profileID:        "default",
		allowAllProfiles: true,
		store:            store,
		queue:            NewExecutionQueue(1, 1, func(context.Context, ExecutionRequest) {}, zerolog.Nop()),
		logger:           zerolog.Nop(),
	}
	for _, tt := range []string{TriggerTypeOrgTool, TriggerTypeOrgMessage} {
		orig := &WorkflowExecution{WorkflowID: "wf-x", Status: "FAILED", TriggerType: tt, TriggerData: map[string]interface{}{}}
		if err := store.CreateExecution(ctx, orig); err != nil {
			t.Fatal(err)
		}
		if _, err := e.RetryExecution(ctx, orig.ID); !errors.Is(err, ErrRetryOrgStarted) {
			t.Errorf("%s: RetryExecution error = %v, want ErrRetryOrgStarted", tt, err)
		}
	}
	manual := &WorkflowExecution{WorkflowID: "wf-missing", Status: "FAILED", TriggerType: "trigger.manual", TriggerData: map[string]interface{}{}}
	if err := store.CreateExecution(ctx, manual); err != nil {
		t.Fatal(err)
	}
	if _, err := e.RetryExecution(ctx, manual.ID); errors.Is(err, ErrRetryOrgStarted) {
		t.Errorf("manual run refused as org-started: %v", err)
	}
}
