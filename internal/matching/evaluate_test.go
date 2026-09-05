// internal/matching/evaluate_test.go
package matching

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/monoes/mono-agent/internal/applications"
	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/storage"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "matching-test.db")
	db, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatalf("NewDatabase: %v", err)
	}
	if err := db.ApplyMigrations(); err != nil {
		t.Fatalf("ApplyMigrations: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db.DB
}

func fakeGoodResponse(ctx context.Context, opts monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
	return &monomind.TurnResult{
		ResultText: `{"eligibility_pass":true,"language_pass":true,"location_pass":true,"technical_score":85,"experience_score":80,"behavioral_score":70,"career_score":90,"overall_score":84.5,"verdict":"Strong Fit","rationale":"Great match."}`,
	}, nil
}

func setupEvaluateTest(t *testing.T) (db *sql.DB, applicationID string) {
	t.Helper()
	db = newTestDB(t)
	store := applications.NewStore(db)
	app := &applications.Application{
		ProfileID: "default", Kind: applications.KindJob,
		Job: &applications.JobDetails{Title: "Backend Engineer", Company: "Acme", URL: "https://acme.example/1", Description: "Go backend role."},
	}
	if err := store.Create(context.Background(), app); err != nil {
		t.Fatalf("Create: %v", err)
	}

	origEnsure := EnsureFunc
	EnsureFunc = func(ctx context.Context) (string, *monomind.VersionInfo, error) { return "/fake/monomind", nil, nil }
	t.Cleanup(func() { EnsureFunc = origEnsure })

	origSearch := SearchKnowledgeFunc
	SearchKnowledgeFunc = func(ctx context.Context, db *sql.DB, profileID, query string) ([]monomind.KnowledgeResult, error) {
		return []monomind.KnowledgeResult{{Path: "/vault/resume.txt", Excerpt: "8 years of Go experience.", Score: 0.9}}, nil
	}
	t.Cleanup(func() { SearchKnowledgeFunc = origSearch })

	return db, app.ID
}

func TestEvaluateCreatesEvaluationAndTag(t *testing.T) {
	db, applicationID := setupEvaluateTest(t)
	origExec := ExecFunc
	ExecFunc = fakeGoodResponse
	t.Cleanup(func() { ExecFunc = origExec })

	verdict, err := Evaluate(context.Background(), db, "default", applicationID, "claude")
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if verdict.Verdict != "Strong Fit" {
		t.Fatalf("unexpected verdict: %+v", verdict)
	}

	store := applications.NewStore(db)
	app, err := store.Get(context.Background(), "default", applicationID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	found := false
	for _, tag := range app.Tags {
		if tag == "fit:strong-fit" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected tag fit:strong-fit, got tags %v", app.Tags)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM application_evaluations WHERE application_id = ?`, applicationID).Scan(&count); err != nil {
		t.Fatalf("counting evaluations: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 evaluation row, got %d", count)
	}
}

func TestEvaluateRejectsNonJobApplications(t *testing.T) {
	db := newTestDB(t)
	store := applications.NewStore(db)
	app := &applications.Application{
		ProfileID: "default", Kind: applications.KindTender,
		Tender: &applications.TenderDetails{Title: "Road Tender", IssuingOrg: "Ministry", URL: "https://t.example/1", SubmissionDeadline: "2026-12-01"},
	}
	if err := store.Create(context.Background(), app); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := Evaluate(context.Background(), db, "default", app.ID, "claude"); err == nil {
		t.Fatal("expected error evaluating a tender application, got nil")
	}
}

func TestEvaluateReEvaluationAppendsNewRow(t *testing.T) {
	db, applicationID := setupEvaluateTest(t)
	origExec := ExecFunc
	ExecFunc = fakeGoodResponse
	t.Cleanup(func() { ExecFunc = origExec })

	if _, err := Evaluate(context.Background(), db, "default", applicationID, "claude"); err != nil {
		t.Fatalf("Evaluate (1st): %v", err)
	}
	if _, err := Evaluate(context.Background(), db, "default", applicationID, "claude"); err != nil {
		t.Fatalf("Evaluate (2nd): %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM application_evaluations WHERE application_id = ?`, applicationID).Scan(&count); err != nil {
		t.Fatalf("counting evaluations: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 evaluation rows after re-evaluating, got %d", count)
	}
}
