package matchingnodes_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/monoes/mono-agent/internal/applications"
	"github.com/monoes/mono-agent/internal/jev/jevtest"
	"github.com/monoes/mono-agent/internal/matching"
	"github.com/monoes/mono-agent/internal/monomind"
	matchingnodes "github.com/monoes/mono-agent/internal/nodes/matching"
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

func TestEvaluateNodeRuntimeJev(t *testing.T) {
	origEnsure := matching.EnsureFunc
	matching.EnsureFunc = func(ctx context.Context) (string, *monomind.VersionInfo, error) { return "/fake/monomind", nil, nil }
	t.Cleanup(func() { matching.EnsureFunc = origEnsure })
	origSearch := matching.SearchKnowledgeFunc
	matching.SearchKnowledgeFunc = func(ctx context.Context, db *sql.DB, profileID, query string) ([]monomind.KnowledgeResult, error) {
		return []monomind.KnowledgeResult{{Path: "/vault/cv.md", Excerpt: "Go engineer."}}, nil
	}
	t.Cleanup(func() { matching.SearchKnowledgeFunc = origSearch })
	origExec := matching.ExecFunc
	matching.ExecFunc = func(ctx context.Context, opts monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		t.Error("runtime jev must not start an agent turn")
		return &monomind.TurnResult{}, nil
	}
	t.Cleanup(func() { matching.ExecFunc = origExec })
	t.Setenv("TYPESAFE_API_KEY", "test-key")
	srv := jevtest.NewServer(t, jevtest.Fixed(map[string]string{
		"eligibility": "0.1", "language": "0.1", "location": "0.1",
		"technical": "3", "experience": "3", "behavioral": "3", "career": "3",
	}))

	db := newTestDB(t)
	matchingnodes.SetGlobalDB(db.DB)
	app := &applications.Application{
		Kind: applications.KindJob,
		Job:  &applications.JobDetails{Title: "Backend Engineer", Company: "Acme", URL: "https://a.example"},
	}
	if err := applications.NewStore(db.DB).Create(context.Background(), app); err != nil {
		t.Fatalf("Create: %v", err)
	}

	outputs, err := (&matchingnodes.EvaluateNode{}).Execute(context.Background(), workflow.NodeInput{},
		map[string]interface{}{"application_id": app.ID, "runtime": "jev"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got := outputs[0].Items[0].JSON
	if got["verdict"] != "Good Fit" || got["overall_score"] != 75.0 {
		t.Fatalf("unexpected output: %+v", got)
	}
	if srv.Calls() != 1 {
		t.Fatalf("want 1 Jev call, got %d", srv.Calls())
	}
	var runtime string
	if err := db.DB.QueryRow(`SELECT runtime FROM application_evaluations WHERE application_id = ?`, app.ID).Scan(&runtime); err != nil {
		t.Fatal(err)
	}
	if runtime != "jev:jev-test" {
		t.Fatalf("stored runtime = %q", runtime)
	}
}
