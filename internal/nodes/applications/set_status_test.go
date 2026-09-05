// internal/nodes/applications/set_status_test.go
package applicationsnodes_test

import (
	"context"
	"testing"

	"github.com/monoes/mono-agent/internal/applications"
	applicationsnodes "github.com/monoes/mono-agent/internal/nodes/applications"
	"github.com/monoes/mono-agent/internal/workflow"
)

func TestSetStatusNodeTransitions(t *testing.T) {
	db := newTestDB(t)
	applicationsnodes.SetGlobalStore(db.DB)
	store := applications.NewStore(db.DB)

	app := &applications.Application{Kind: applications.KindJob, Job: &applications.JobDetails{Title: "Backend Engineer", Company: "Acme", URL: "https://a.example"}}
	if err := store.Create(context.Background(), app); err != nil {
		t.Fatalf("Create: %v", err)
	}

	node := &applicationsnodes.SetStatusNode{}
	config := map[string]interface{}{"id": app.ID, "status": "cancelled", "note": "no longer interested"}
	outputs, err := node.Execute(context.Background(), workflow.NodeInput{}, config)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	status, _ := outputs[0].Items[0].JSON["status"].(string)
	if status != "cancelled" {
		t.Fatalf("expected status cancelled in output, got %q", status)
	}
}

// TestSetStatusNodeRejectsApplied guards the invariant that `application
// send` (a human-invoked CLI command) is the only way an application
// becomes "applied" — an unattended workflow must never be able to reach
// that status on its own, even though pending->applied is otherwise a
// valid edge in the store's transition graph.
func TestSetStatusNodeRejectsApplied(t *testing.T) {
	db := newTestDB(t)
	applicationsnodes.SetGlobalStore(db.DB)
	store := applications.NewStore(db.DB)

	app := &applications.Application{Kind: applications.KindJob, Job: &applications.JobDetails{Title: "Backend Engineer", Company: "Acme", URL: "https://a.example"}}
	if err := store.Create(context.Background(), app); err != nil {
		t.Fatalf("Create: %v", err)
	}

	node := &applicationsnodes.SetStatusNode{}
	config := map[string]interface{}{"id": app.ID, "status": "applied"}
	if _, err := node.Execute(context.Background(), workflow.NodeInput{}, config); err == nil {
		t.Fatal("expected error setting status to \"applied\" via a workflow node, got nil")
	}

	got, err := store.Get(context.Background(), "default", app.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != applications.StatusPending {
		t.Fatalf("expected application to remain pending after the rejected attempt, got %q", got.Status)
	}
}

func TestSetStatusNodeRejectsInvalidTransition(t *testing.T) {
	db := newTestDB(t)
	applicationsnodes.SetGlobalStore(db.DB)
	store := applications.NewStore(db.DB)

	app := &applications.Application{Kind: applications.KindJob, Job: &applications.JobDetails{Title: "Backend Engineer", Company: "Acme", URL: "https://a.example"}}
	if err := store.Create(context.Background(), app); err != nil {
		t.Fatalf("Create: %v", err)
	}

	node := &applicationsnodes.SetStatusNode{}
	config := map[string]interface{}{"id": app.ID, "status": "rejected"} // pending->rejected is not a valid edge
	if _, err := node.Execute(context.Background(), workflow.NodeInput{}, config); err == nil {
		t.Fatal("expected error for invalid transition, got nil")
	}
}
