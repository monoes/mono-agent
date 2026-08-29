package main

import (
	"path/filepath"
	"testing"

	"github.com/monoes/mono-agent/internal/storage"
)

// newRunTestDB creates a migrated database with one action row and returns it.
func newRunTestDB(t *testing.T, actionID string, reachedIndex int) *storage.Database {
	t.Helper()
	db, err := storage.NewDatabase(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("creating database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.ApplyMigrations(); err != nil {
		t.Fatalf("applying migrations: %v", err)
	}
	if _, err := db.DB.Exec(
		`INSERT INTO actions (id, created_at, title, type, state, target_platform, reached_index, profile_id)
		 VALUES (?, ?, ?, ?, 'RUNNING', 'instagram', ?, 'default')`,
		actionID, 1, "Test action", "send_dms", reachedIndex,
	); err != nil {
		t.Fatalf("inserting test action: %v", err)
	}
	return db
}

func getReachedIndex(t *testing.T, db *storage.Database, actionID string) int {
	t.Helper()
	var idx int
	if err := db.DB.QueryRow(
		"SELECT reached_index FROM actions WHERE id = ?", actionID,
	).Scan(&idx); err != nil {
		t.Fatalf("reading reached_index: %v", err)
	}
	return idx
}

// TestFinalizeActionStateDoesNotRegressReachedIndex verifies that a resumed
// run which processed fewer items than a previous attempt keeps the persisted
// high-water mark — a simulated resume must not restart from 0 (RB2-2).
func TestFinalizeActionStateDoesNotRegressReachedIndex(t *testing.T) {
	db := newRunTestDB(t, "act-resume", 7)

	// Simulated resume: previous run reached index 7; this run processed
	// only 3 items and failed. The old code wrote 3 (or 0), corrupting the
	// resume point and re-processing already-handled items.
	if err := finalizeActionState(db, "", "act-resume", "FAILED", 3); err != nil {
		t.Fatalf("finalizeActionState: %v", err)
	}
	if got := getReachedIndex(t, db, "act-resume"); got != 7 {
		t.Fatalf("resume with 3 processed must not lower reached_index from 7, got %d", got)
	}

	// A run that advances further moves the high-water mark forward.
	if err := finalizeActionState(db, "", "act-resume", "FAILED", 9); err != nil {
		t.Fatalf("finalizeActionState: %v", err)
	}
	if got := getReachedIndex(t, db, "act-resume"); got != 9 {
		t.Fatalf("expected reached_index to advance to 9, got %d", got)
	}

	// Even 0 processed on a failure must not zero out previous progress.
	if err := finalizeActionState(db, "", "act-resume", "FAILED", 0); err != nil {
		t.Fatalf("finalizeActionState: %v", err)
	}
	if got := getReachedIndex(t, db, "act-resume"); got != 9 {
		t.Fatalf("failed run with 0 processed must keep reached_index 9, got %d", got)
	}

	// State and execution count are still updated.
	var state string
	var count int
	if err := db.DB.QueryRow(
		"SELECT state, action_execution_count FROM actions WHERE id = ?", "act-resume",
	).Scan(&state, &count); err != nil {
		t.Fatalf("reading action row: %v", err)
	}
	if state != "FAILED" || count != 3 {
		t.Fatalf("expected state FAILED and execution count 3, got %s/%d", state, count)
	}
}

// TestFinalizeActionStateCompletedEmptyResetsIndex verifies the re-run
// escape hatch: a completed run that processed nothing resets the index so
// the action can be executed again from the start.
func TestFinalizeActionStateCompletedEmptyResetsIndex(t *testing.T) {
	db := newRunTestDB(t, "act-done", 7)

	if err := finalizeActionState(db, "", "act-done", "COMPLETED", 0); err != nil {
		t.Fatalf("finalizeActionState: %v", err)
	}
	if got := getReachedIndex(t, db, "act-done"); got != 0 {
		t.Fatalf("completed empty run should reset reached_index to 0, got %d", got)
	}

	// A completed run that processed items keeps the high-water mark.
	if err := finalizeActionState(db, "", "act-done", "COMPLETED", 5); err != nil {
		t.Fatalf("finalizeActionState: %v", err)
	}
	if got := getReachedIndex(t, db, "act-done"); got != 5 {
		t.Fatalf("completed run with 5 processed should persist 5, got %d", got)
	}
}

// TestStorageAdapterReachedIndexMonotonic verifies the executor's mid-loop
// progress writes are also monotonic.
func TestStorageAdapterReachedIndexMonotonic(t *testing.T) {
	db := newRunTestDB(t, "act-adapter", 5)
	sa := &storageAdapter{db: db, profileID: "default"}

	if err := sa.UpdateActionReachedIndex("act-adapter", 2); err != nil {
		t.Fatalf("UpdateActionReachedIndex: %v", err)
	}
	if got := getReachedIndex(t, db, "act-adapter"); got != 5 {
		t.Fatalf("downward index write must be ignored: expected 5, got %d", got)
	}

	if err := sa.UpdateActionReachedIndex("act-adapter", 8); err != nil {
		t.Fatalf("UpdateActionReachedIndex: %v", err)
	}
	if got := getReachedIndex(t, db, "act-adapter"); got != 8 {
		t.Fatalf("upward index write must apply: expected 8, got %d", got)
	}
}
