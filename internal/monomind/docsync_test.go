package monomind_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/storage"
)

func newDocsyncTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "docsync-test.db")
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

func setFakeMonomindOnPath(t *testing.T) {
	t.Helper()
	fakeDir := t.TempDir()
	src, err := filepath.Abs("testdata/fake-monomind.sh")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(src, 0o755); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(fakeDir, "monomind")
	if err := os.Symlink(src, linkPath); err != nil {
		t.Fatalf("symlink fake monomind onto PATH: %v", err)
	}
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestIngestDocumentSucceeds(t *testing.T) {
	setFakeMonomindOnPath(t)
	db := newDocsyncTestDB(t)
	docPath := filepath.Join(t.TempDir(), "resume.txt")
	if err := os.WriteFile(docPath, []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := monomind.IngestDocument(context.Background(), db, "default", docPath); err != nil {
		t.Fatalf("IngestDocument: %v", err)
	}
}

func TestIngestDocumentPropagatesFailure(t *testing.T) {
	setFakeMonomindOnPath(t)
	t.Setenv("INGEST_FAIL", "1")
	db := newDocsyncTestDB(t)

	if err := monomind.IngestDocument(context.Background(), db, "default", "/fake/path"); err == nil {
		t.Fatal("expected error when the fake binary reports failure, got nil")
	}
}

// TestIngestDocumentDetectsToolLevelFailure guards against a real bug: the
// monomind subprocess can exit 0 while its own JSON envelope reports the
// tool call failed (e.g. its path-traversal guard rejecting a path outside
// the resolved project root) -- IngestDocument must not treat exit code 0
// as success on its own.
func TestIngestDocumentDetectsToolLevelFailure(t *testing.T) {
	setFakeMonomindOnPath(t)
	t.Setenv("INGEST_TOOL_ERROR", "1")
	db := newDocsyncTestDB(t)

	err := monomind.IngestDocument(context.Background(), db, "default", "/fake/path")
	if err == nil {
		t.Fatal("expected an error when the tool reports isError/success:false despite exit code 0, got nil")
	}
	if !strings.Contains(err.Error(), "Absolute path must not escape") {
		t.Fatalf("expected the tool's own error message surfaced, got: %v", err)
	}
}

func TestSearchKnowledgeReturnsExcerptsOnly(t *testing.T) {
	setFakeMonomindOnPath(t)
	t.Setenv("SEARCH_CAPTURES_FAIL", "1") // documents store only
	db := newDocsyncTestDB(t)

	results, err := monomind.SearchKnowledge(context.Background(), db, "default", "backend engineer")
	if err != nil {
		t.Fatalf("SearchKnowledge: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 excerpt result (the fake's \"rule\" kind entry must be filtered out), got %d: %+v", len(results), results)
	}
	if results[0].Path != "/fake/resume.txt" {
		t.Fatalf("unexpected path: %q", results[0].Path)
	}
	if results[0].Excerpt != "Experienced backend engineer with 8 years in distributed systems." {
		t.Fatalf("unexpected excerpt: %q", results[0].Excerpt)
	}
	if results[0].Score != 0.91 {
		t.Fatalf("unexpected score: %v", results[0].Score)
	}
}

// TestSearchKnowledgeIncludesProfileCaptures: browser captures live in the
// profile's own capture store (scope profile:<id>), not in the documents
// store, so chat search must ask both and merge them by score.
func TestSearchKnowledgeIncludesProfileCaptures(t *testing.T) {
	setFakeMonomindOnPath(t)
	log := filepath.Join(t.TempDir(), "mcp.log")
	t.Setenv("FAKE_MCP_LOG", log)
	db := newDocsyncTestDB(t)

	results, err := monomind.SearchKnowledge(context.Background(), db, "work", "distributed systems")
	if err != nil {
		t.Fatalf("SearchKnowledge: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("want the capture and the document, got %+v", results)
	}
	if results[0].Path != "/fake/inbox/cap/readable.md" || results[1].Path != "/fake/resume.txt" {
		t.Fatalf("want results merged by score (capture 0.95 first), got %+v", results)
	}
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	calls := string(raw)
	if !strings.Contains(calls, `"scope":"profile:work"`) || !strings.Contains(calls, `"surfaces":["chunks"]`) {
		t.Fatalf("capture search must name the profile's scope and only the chunk surface; calls:\n%s", calls)
	}
	if strings.Contains(calls, `"scope":"profile:default"`) {
		t.Fatalf("searched another profile's captures:\n%s", calls)
	}
}

// TestSearchKnowledgeOneStoreFailing keeps the documents' answers when the
// capture store cannot be searched, and fails only when both fail.
func TestSearchKnowledgeOneStoreFailing(t *testing.T) {
	setFakeMonomindOnPath(t)
	t.Setenv("SEARCH_CAPTURES_FAIL", "1")
	db := newDocsyncTestDB(t)
	results, err := monomind.SearchKnowledge(context.Background(), db, "work", "x")
	if err != nil || len(results) != 1 {
		t.Fatalf("want the documents store's result despite the capture store failing, got %+v, %v", results, err)
	}
}

func TestIngestCaptureNamesProfileScope(t *testing.T) {
	setFakeMonomindOnPath(t)
	log := filepath.Join(t.TempDir(), "mcp.log")
	t.Setenv("FAKE_MCP_LOG", log)

	if err := monomind.IngestCapture(context.Background(), "work", "/fake/inbox/cap/readable.md"); err != nil {
		t.Fatalf("IngestCapture: %v", err)
	}
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"scope":"profile:work"`) {
		t.Fatalf("ingest must name the profile's capture scope, got %s", raw)
	}
	if err := monomind.IngestCapture(context.Background(), "../x", "/p"); err == nil {
		t.Fatal("a traversal id must be refused before any exec")
	}
}

func TestCaptureScope(t *testing.T) {
	for id, want := range map[string]string{"work": "profile:work", " a1 ": "profile:a1", "": "", "../x": "", "a/b": ""} {
		if got := monomind.CaptureScope(id); got != want {
			t.Errorf("CaptureScope(%q) = %q, want %q", id, got, want)
		}
	}
}
