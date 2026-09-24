package storage

import (
	"context"
	"path/filepath"
	"testing"
)

func TestPendingMigrations(t *testing.T) {
	ctx := context.Background()
	db, err := NewDatabase(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	all, err := embeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	pending, err := db.PendingMigrations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != len(all) || len(all) == 0 {
		t.Fatalf("fresh db: pending = %d, want all %d", len(pending), len(all))
	}

	if err := db.ApplyMigrations(); err != nil {
		t.Fatal(err)
	}
	pending, err = db.PendingMigrations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("migrated db: pending = %v, want none", pending)
	}

	if msg, err := db.QuickCheck(ctx); err != nil || msg != "" {
		t.Fatalf("QuickCheck = %q, %v; want intact", msg, err)
	}
}
