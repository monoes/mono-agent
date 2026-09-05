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
	config := map[string]interface{}{"id": app.ID, "status": "applied", "note": "sent"}
	outputs, err := node.Execute(context.Background(), workflow.NodeInput{}, config)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	status, _ := outputs[0].Items[0].JSON["status"].(string)
	if status != "applied" {
		t.Fatalf("expected status applied in output, got %q", status)
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
