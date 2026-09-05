package discoverynodes_test

import (
	"context"
	"path/filepath"
	"testing"

	discoverynodes "github.com/monoes/mono-agent/internal/nodes/discovery"
	"github.com/monoes/mono-agent/internal/storage"
	"github.com/monoes/mono-agent/internal/workflow"
)

func newTestDB(t *testing.T) *storage.Database {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "discovery-node-test.db")
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

func TestSearchJobsNodeRejectsUnknownSource(t *testing.T) {
	db := newTestDB(t)
	discoverynodes.SetGlobalStore(db.DB)

	node := &discoverynodes.SearchJobsNode{}
	if node.Type() != "discovery.search_jobs" {
		t.Fatalf("expected type discovery.search_jobs, got %q", node.Type())
	}
	config := map[string]interface{}{"keywords": "engineer", "source": "does-not-exist"}
	if _, err := node.Execute(context.Background(), workflow.NodeInput{}, config); err == nil {
		t.Fatal("expected error for unknown source, got nil")
	}
}

func TestSearchJobsNodeRequiresKeywords(t *testing.T) {
	db := newTestDB(t)
	discoverynodes.SetGlobalStore(db.DB)

	node := &discoverynodes.SearchJobsNode{}
	if _, err := node.Execute(context.Background(), workflow.NodeInput{}, map[string]interface{}{}); err == nil {
		t.Fatal("expected error for missing keywords, got nil")
	}
}
