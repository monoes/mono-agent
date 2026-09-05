package monomind_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
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

func TestSearchKnowledgeReturnsExcerptsOnly(t *testing.T) {
	setFakeMonomindOnPath(t)
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
