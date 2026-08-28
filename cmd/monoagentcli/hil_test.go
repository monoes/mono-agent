package main

import (
	"path/filepath"
	"testing"

	"github.com/monoes/mono-agent/internal/storage"
)

func newHILTestDB(t *testing.T) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "hil-test.db")
	db, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatalf("NewDatabase: %v", err)
	}
	if err := db.ApplyMigrations(); err != nil {
		t.Fatalf("ApplyMigrations: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("closing seed db: %v", err)
	}
	return dbPath
}

// TestHILApproveUnknownIDIsNotFound guards RV4-4: approving an unknown (or
// already resolved) HIL item must exit 2, not 1.
func TestHILApproveUnknownIDIsNotFound(t *testing.T) {
	cfg := &globalConfig{DBPath: newHILTestDB(t), ProfileID: "default"}
	cmd := newHILApproveCmd(cfg)
	cmd.SetArgs([]string{"deadbeef"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for unknown HIL item")
	}
	if code := exitCodeFor(err); code != 2 {
		t.Fatalf("expected exit code 2 for unknown HIL item, got %d (%v)", code, err)
	}
}

// TestHILRejectUnknownIDIsNotFound is TestHILApproveUnknownIDIsNotFound's
// reject counterpart.
func TestHILRejectUnknownIDIsNotFound(t *testing.T) {
	cfg := &globalConfig{DBPath: newHILTestDB(t), ProfileID: "default"}
	cmd := newHILRejectCmd(cfg)
	cmd.SetArgs([]string{"deadbeef"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for unknown HIL item")
	}
	if code := exitCodeFor(err); code != 2 {
		t.Fatalf("expected exit code 2 for unknown HIL item, got %d (%v)", code, err)
	}
}
