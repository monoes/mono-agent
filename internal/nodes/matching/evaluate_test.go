package matchingnodes_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/monoes/mono-agent/internal/applications"
	"github.com/monoes/mono-agent/internal/matching"
	matchingnodes "github.com/monoes/mono-agent/internal/nodes/matching"
	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/storage"
	"github.com/monoes/mono-agent/internal/workflow"
)

func newTestDB(t *testing.T) *storage.Database {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "matching-node-test.db")
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

func TestEvaluateNodeScoresApplication(t *testing.T) {
	origEnsure := matching.EnsureFunc
	matching.EnsureFunc = func(ctx context.Context) (string, *monomind.VersionInfo, error) { return "/fake/monomind", nil, nil }
	t.Cleanup(func() { matching.EnsureFunc = origEnsure })
	origSearch := matching.SearchKnowledgeFunc
	matching.SearchKnowledgeFunc = func(ctx context.Context, db *sql.DB, profileID, query string) ([]monomind.KnowledgeResult, error) {
		return nil, nil
	}
	t.Cleanup(func() { matching.SearchKnowledgeFunc = origSearch })
	origExec := matching.ExecFunc
	matching.ExecFunc = func(ctx context.Context, opts monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		return &monomind.TurnResult{ResultText: `{"eligibility_pass":true,"language_pass":true,"location_pass":true,"technical_score":80,"experience_score":80,"behavioral_score":80,"career_score":80,"overall_score":80,"verdict":"Strong Fit","rationale":"Good."}`}, nil
	}
	t.Cleanup(func() { matching.ExecFunc = origExec })

	db := newTestDB(t)
	matchingnodes.SetGlobalDB(db.DB)
	store := applications.NewStore(db.DB)
	app := &applications.Application{
		Kind: applications.KindJob,
		Job:  &applications.JobDetails{Title: "Backend Engineer", Company: "Acme", URL: "https://a.example"},
	}
	if err := store.Create(context.Background(), app); err != nil {
		t.Fatalf("Create: %v", err)
	}

	node := &matchingnodes.EvaluateNode{}
	if node.Type() != "applications.evaluate" {
		t.Fatalf("expected type applications.evaluate, got %q", node.Type())
	}
	outputs, err := node.Execute(context.Background(), workflow.NodeInput{}, map[string]interface{}{"application_id": app.ID})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if outputs[0].Items[0].JSON["verdict"] != "Strong Fit" {
		t.Fatalf("unexpected output: %+v", outputs[0].Items[0].JSON)
	}
}
