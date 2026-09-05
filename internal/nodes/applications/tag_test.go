// internal/nodes/applications/tag_test.go
package applicationsnodes_test

import (
	"context"
	"testing"

	"github.com/monoes/mono-agent/internal/applications"
	applicationsnodes "github.com/monoes/mono-agent/internal/nodes/applications"
	"github.com/monoes/mono-agent/internal/workflow"
)

func TestTagNodeAddAndRemove(t *testing.T) {
	db := newTestDB(t)
	applicationsnodes.SetGlobalStore(db.DB)
	store := applications.NewStore(db.DB)

	app := &applications.Application{Kind: applications.KindJob, Job: &applications.JobDetails{Title: "Backend Engineer", Company: "Acme", URL: "https://a.example"}}
	if err := store.Create(context.Background(), app); err != nil {
		t.Fatalf("Create: %v", err)
	}

	node := &applicationsnodes.TagNode{}
	addCfg := map[string]interface{}{"id": app.ID, "tag": "urgent", "action": "add"}
	if _, err := node.Execute(context.Background(), workflow.NodeInput{}, addCfg); err != nil {
		t.Fatalf("Execute (add): %v", err)
	}
	got, err := store.Get(context.Background(), "default", app.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Tags) != 1 || got.Tags[0] != "urgent" {
		t.Fatalf("expected tag urgent, got %v", got.Tags)
	}

	removeCfg := map[string]interface{}{"id": app.ID, "tag": "urgent", "action": "remove"}
	if _, err := node.Execute(context.Background(), workflow.NodeInput{}, removeCfg); err != nil {
		t.Fatalf("Execute (remove): %v", err)
	}
	got, err = store.Get(context.Background(), "default", app.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Tags) != 0 {
		t.Fatalf("expected no tags, got %v", got.Tags)
	}
}
