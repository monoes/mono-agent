// internal/applications/store_test.go
package applications_test

import (
	"context"
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
