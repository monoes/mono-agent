// internal/nodes/applications/create_test.go
package applicationsnodes_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/monoes/mono-agent/internal/applications"
	applicationsnodes "github.com/monoes/mono-agent/internal/nodes/applications"
	"github.com/monoes/mono-agent/internal/storage"
	"github.com/monoes/mono-agent/internal/workflow"
)

func newTestDB(t *testing.T) *storage.Database {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "applications-nodes-test.db")
	db, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatalf("NewDatabase: %v", err)
	}
	if err := db.ApplyMigrations(); err != nil {
		t.Fatalf("ApplyMigrations: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestCreateNodeCreatesJob(t *testing.T) {
	db := newTestDB(t)
	applicationsnodes.SetGlobalStore(db.DB)

	node := &applicationsnodes.CreateNode{}
	if node.Type() != "applications.create" {
		t.Fatalf("expected type applications.create, got %q", node.Type())
	}

	config := map[string]interface{}{
		"kind":    "job",
		"company": "Acme Corp",
		"url":     "https://acme.example/jobs/1",
	}
	outputs, err := node.Execute(context.Background(), workflow.NodeInput{}, config)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(outputs) != 1 || len(outputs[0].Items) != 1 {
		t.Fatalf("expected exactly one output item, got %+v", outputs)
	}
	id, _ := outputs[0].Items[0].JSON["id"].(string)
	if id == "" {
		t.Fatal("output item missing id")
	}

	store := applications.NewStore(db.DB)
	got, err := store.Get(context.Background(), "default", id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Job == nil || got.Job.Company != "Acme Corp" {
		t.Fatalf("job not persisted correctly: %+v", got.Job)
	}
}

func TestCreateNodeRejectsMissingFields(t *testing.T) {
	db := newTestDB(t)
	applicationsnodes.SetGlobalStore(db.DB)

	node := &applicationsnodes.CreateNode{}
	config := map[string]interface{}{"kind": "job", "company": "Acme"} // missing url
	if _, err := node.Execute(context.Background(), workflow.NodeInput{}, config); err == nil {
		t.Fatal("expected error for missing url, got nil")
	}
}
