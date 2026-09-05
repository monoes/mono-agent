package discovery_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/monoes/mono-agent/internal/applications"
	"github.com/monoes/mono-agent/internal/discovery"
	"github.com/monoes/mono-agent/internal/storage"
)

func newTestDB(t *testing.T) *storage.Database {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "discovery-test.db")
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

func TestIsDuplicateURLMatch(t *testing.T) {
	db := newTestDB(t)
	store := applications.NewStore(db.DB)
	ctx := context.Background()

	app := &applications.Application{
		ProfileID: "default", Kind: applications.KindJob,
		Job: &applications.JobDetails{Title: "Backend Engineer", Company: "Acme", URL: "https://acme.example/jobs/1"},
	}
	if err := store.Create(ctx, app); err != nil {
		t.Fatalf("Create: %v", err)
	}

	dup, err := discovery.IsDuplicate(ctx, store, "default", discovery.Result{
		Title: "Something Else Entirely", Company: "Different Co", URL: "https://acme.example/jobs/1",
	})
	if err != nil {
		t.Fatalf("IsDuplicate: %v", err)
	}
	if !dup {
		t.Fatal("expected URL match to be flagged as duplicate")
	}
}

func TestIsDuplicateNormalizedTitleCompanyMatch(t *testing.T) {
	db := newTestDB(t)
	store := applications.NewStore(db.DB)
	ctx := context.Background()

	app := &applications.Application{
		ProfileID: "default", Kind: applications.KindJob,
		Job: &applications.JobDetails{Title: "Senior Backend Engineer!", Company: "Acme Corp.", URL: "https://acme.example/jobs/1"},
	}
	if err := store.Create(ctx, app); err != nil {
		t.Fatalf("Create: %v", err)
	}

	dup, err := discovery.IsDuplicate(ctx, store, "default", discovery.Result{
		Title: "senior backend engineer", Company: "acme corp", URL: "https://acme.example/jobs/999-different",
	})
	if err != nil {
		t.Fatalf("IsDuplicate: %v", err)
	}
	if !dup {
		t.Fatal("expected normalized title+company match to be flagged as duplicate")
	}
}

func TestIsDuplicateNoFalsePositive(t *testing.T) {
	db := newTestDB(t)
	store := applications.NewStore(db.DB)
	ctx := context.Background()

	app := &applications.Application{
		ProfileID: "default", Kind: applications.KindJob,
		Job: &applications.JobDetails{Title: "Backend Engineer", Company: "Acme", URL: "https://acme.example/jobs/1"},
	}
	if err := store.Create(ctx, app); err != nil {
		t.Fatalf("Create: %v", err)
	}

	dup, err := discovery.IsDuplicate(ctx, store, "default", discovery.Result{
		Title: "Frontend Engineer", Company: "Acme", URL: "https://acme.example/jobs/2",
	})
	if err != nil {
		t.Fatalf("IsDuplicate: %v", err)
	}
	if dup {
		t.Fatal("expected a genuinely different posting not to be flagged as duplicate")
	}
}
