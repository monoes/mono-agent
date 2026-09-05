// internal/applications/applications_test.go
package applications_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/monoes/mono-agent/internal/storage"
)

func newTestDB(t *testing.T) *storage.Database {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "applications-test.db")
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

func TestMigrationCreatesAllTables(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	for _, table := range []string{
		"applications", "application_tags", "application_status_log",
		"job_details", "tender_details",
	} {
		var name string
		err := db.DB.QueryRowContext(ctx,
			`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table,
		).Scan(&name)
		if err != nil {
			t.Fatalf("table %q not created by migration: %v", table, err)
		}
	}
}
