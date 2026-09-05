// internal/applications/store_test.go
package applications_test

import (
	"context"
	"errors"
	"testing"

	"github.com/monoes/mono-agent/internal/applications"
)

func TestStoreCreateJob(t *testing.T) {
	db := newTestDB(t)
	store := applications.NewStore(db.DB)
	ctx := context.Background()

	app := &applications.Application{
		ProfileID: "default",
		Kind:      applications.KindJob,
		Job: &applications.JobDetails{
			Company: "Acme Corp",
			URL:     "https://acme.example/jobs/123",
		},
	}
	if err := store.Create(ctx, app); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if app.ID == "" {
		t.Fatal("Create did not set app.ID")
	}
	if app.Status != applications.StatusPending {
		t.Fatalf("expected status pending, got %q", app.Status)
	}
	if app.CreatedAt == "" || app.UpdatedAt == "" {
		t.Fatal("Create did not set timestamps")
	}
}

func TestStoreCreateTender(t *testing.T) {
	db := newTestDB(t)
	store := applications.NewStore(db.DB)
	ctx := context.Background()

	app := &applications.Application{
		ProfileID: "default",
		Kind:      applications.KindTender,
		Tender: &applications.TenderDetails{
			IssuingOrg:         "Ministry of Example",
			URL:                "https://tenders.example/t/456",
			SubmissionDeadline: "2026-12-01",
		},
	}
	if err := store.Create(ctx, app); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if app.ID == "" {
		t.Fatal("Create did not set app.ID")
	}
}

func TestStoreCreateRejectsMissingJobFields(t *testing.T) {
	db := newTestDB(t)
	store := applications.NewStore(db.DB)
	ctx := context.Background()

	app := &applications.Application{
		ProfileID: "default",
		Kind:      applications.KindJob,
		Job:       &applications.JobDetails{Company: "Acme Corp"}, // missing URL
	}
	if err := store.Create(ctx, app); err == nil {
		t.Fatal("expected error for missing job URL, got nil")
	}
}

func TestStoreCreateRejectsMissingTenderFields(t *testing.T) {
	db := newTestDB(t)
	store := applications.NewStore(db.DB)
	ctx := context.Background()

	app := &applications.Application{
		ProfileID: "default",
		Kind:      applications.KindTender,
		Tender:    &applications.TenderDetails{IssuingOrg: "Ministry"}, // missing URL + deadline
	}
	if err := store.Create(ctx, app); err == nil {
		t.Fatal("expected error for missing tender fields, got nil")
	}
}

func TestStoreCreateRejectsKindDetailMismatch(t *testing.T) {
	db := newTestDB(t)
	store := applications.NewStore(db.DB)
	ctx := context.Background()

	// Kind is job but Tender details supplied instead of Job.
	app := &applications.Application{
		ProfileID: "default",
		Kind:      applications.KindJob,
		Tender:    &applications.TenderDetails{IssuingOrg: "X", URL: "https://x", SubmissionDeadline: "2026-01-01"},
	}
	if err := store.Create(ctx, app); err == nil {
		t.Fatal("expected error for kind/detail mismatch, got nil")
	}
}

func TestStoreCreateRejectsUnknownKind(t *testing.T) {
	db := newTestDB(t)
	store := applications.NewStore(db.DB)
	ctx := context.Background()

	app := &applications.Application{ProfileID: "default", Kind: applications.Kind("grant")}
	if err := store.Create(ctx, app); err == nil {
		t.Fatal("expected error for unknown kind, got nil")
	}
}

func createTestJob(t *testing.T, store *applications.Store) *applications.Application {
	t.Helper()
	app := &applications.Application{
		ProfileID: "default",
		Kind:      applications.KindJob,
		Job:       &applications.JobDetails{Company: "Acme", URL: "https://acme.example/1"},
	}
	if err := store.Create(context.Background(), app); err != nil {
		t.Fatalf("Create: %v", err)
	}
	return app
}

func TestStoreSetStatusValidTransitions(t *testing.T) {
	db := newTestDB(t)
	store := applications.NewStore(db.DB)
	ctx := context.Background()
	app := createTestJob(t, store)

	if err := store.SetStatus(ctx, "default", app.ID, applications.StatusApplied, applications.ActorUser, "sent"); err != nil {
		t.Fatalf("pending->applied: %v", err)
	}
	if err := store.SetStatus(ctx, "default", app.ID, applications.StatusRejected, applications.ActorUser, "no fit"); err != nil {
		t.Fatalf("applied->rejected: %v", err)
	}

	log, err := store.StatusLog(ctx, "default", app.ID)
	if err != nil {
		t.Fatalf("StatusLog: %v", err)
	}
	if len(log) != 3 { // created, applied, rejected
		t.Fatalf("expected 3 ledger entries, got %d", len(log))
	}
}

func TestStoreSetStatusRejectsInvalidTransition(t *testing.T) {
	db := newTestDB(t)
	store := applications.NewStore(db.DB)
	ctx := context.Background()
	app := createTestJob(t, store)

	if err := store.SetStatus(ctx, "default", app.ID, applications.StatusRejected, applications.ActorUser, ""); err == nil {
		t.Fatal("expected error for pending->rejected (not a valid edge), got nil")
	}
	if !errors.Is(mustSetStatusErr(t, store, app.ID), applications.ErrInvalidTransition) {
		t.Fatal("expected ErrInvalidTransition")
	}

	log, err := store.StatusLog(ctx, "default", app.ID)
	if err != nil {
		t.Fatalf("StatusLog: %v", err)
	}
	if len(log) != 1 { // only the initial "created" entry — nothing appended on failure
		t.Fatalf("expected 1 ledger entry after failed transition, got %d", len(log))
	}
}

func mustSetStatusErr(t *testing.T, store *applications.Store, id string) error {
	t.Helper()
	return store.SetStatus(context.Background(), "default", id, applications.StatusRejected, applications.ActorUser, "")
}

func TestStoreSetStatusRejectsFromTerminalState(t *testing.T) {
	db := newTestDB(t)
	store := applications.NewStore(db.DB)
	ctx := context.Background()
	app := createTestJob(t, store)

	if err := store.SetStatus(ctx, "default", app.ID, applications.StatusCancelled, applications.ActorUser, ""); err != nil {
		t.Fatalf("pending->cancelled: %v", err)
	}
	if err := store.SetStatus(ctx, "default", app.ID, applications.StatusApplied, applications.ActorUser, ""); err == nil {
		t.Fatal("expected error transitioning out of terminal state cancelled, got nil")
	}
}

func TestStoreSetStatusNotFound(t *testing.T) {
	db := newTestDB(t)
	store := applications.NewStore(db.DB)
	err := store.SetStatus(context.Background(), "default", "does-not-exist", applications.StatusApplied, applications.ActorUser, "")
	if !errors.Is(err, applications.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
