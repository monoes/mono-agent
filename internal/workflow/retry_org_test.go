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

// A webhook run's chain was admitted through the ledger when the request
// arrived; a retry is not, so it starts a fresh chain instead of continuing
// that one uncounted. The rest of the payload is kept.
func TestRetryExecutionDropsAWebhookRunsChain(t *testing.T) {
	store := newFullEngineStore(t)
	ctx := context.Background()
	wf := &Workflow{Name: "hook", ProfileID: "default", IsActive: true}
	if err := store.CreateWorkflow(ctx, wf); err != nil {
		t.Fatal(err)
	}
	e := &WorkflowEngine{
		profileID:        "default",
		allowAllProfiles: true,
		store:            store,
		queue:            NewExecutionQueue(1, 1, func(context.Context, ExecutionRequest) {}, zerolog.Nop()),
		logger:           zerolog.Nop(),
	}
	orig := &WorkflowExecution{WorkflowID: wf.ID, Status: "FAILED", TriggerType: TriggerNodeTypeWebhook, TriggerData: map[string]interface{}{
		"x": "kept", WebhookTraceKey: map[string]interface{}{"chain_id": "chn_loop", "hop": 4},
	}}
	if err := store.CreateExecution(ctx, orig); err != nil {
		t.Fatal(err)
	}
	id, err := e.RetryExecution(ctx, orig.ID)
	if err != nil {
		t.Fatalf("RetryExecution: %v", err)
	}
	retried, err := store.GetExecution(ctx, id)
	if err != nil || retried == nil {
		t.Fatalf("GetExecution: %v", err)
	}
	if err := retried.ParseTriggerData(); err != nil {
		t.Fatal(err)
	}
	if _, present := retried.TriggerData[WebhookTraceKey]; present || retried.TriggerData["x"] != "kept" {
		t.Fatalf("retried trigger data = %v", retried.TriggerData)
	}
	if _, present := orig.TriggerData[WebhookTraceKey]; !present {
		t.Fatal("the original run's trigger data was modified")
	}
}
