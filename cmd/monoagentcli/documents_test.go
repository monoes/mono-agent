package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/documents"
	"github.com/monoes/mono-agent/internal/storage"
)

func newDocumentsCLITestDB(t *testing.T) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "cli-documents-test.db")
	db, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatalf("NewDatabase: %v", err)
	}
	if err := db.ApplyMigrations(); err != nil {
		t.Fatalf("ApplyMigrations: %v", err)
	}
	if err := db.DB.Close(); err != nil {
		t.Fatalf("closing seed db: %v", err)
	}
	return dbPath
}

func runDocumentsCmd(t *testing.T, dbPath string, args ...string) (string, error) {
	t.Helper()
	cfg := &globalConfig{DBPath: dbPath, JSONOutput: true}
	cmd := newDocumentsCmd(cfg)
	cmd.SetArgs(args)
	var out bytes.Buffer
	cmd.SetOut(&out)
	err := cmd.Execute()
	return out.String(), err
}

func TestDocumentsRenderCV(t *testing.T) {
	orig := documents.RenderPDFFunc
	documents.RenderPDFFunc = func(ctx context.Context, html string) ([]byte, error) { return []byte("%PDF-fake"), nil }
	t.Cleanup(func() { documents.RenderPDFFunc = orig })

	dbPath := newDocumentsCLITestDB(t)
	dataFile := filepath.Join(t.TempDir(), "cv.json")
	if err := os.WriteFile(dataFile, []byte(`{"name":"Jane Doe","title":"Backend Engineer"}`), 0644); err != nil {
		t.Fatal(err)
	}

	out, err := runDocumentsCmd(t, dbPath, "render", "--type", "cv", "--data-file", dataFile)
	if err != nil {
		t.Fatalf("documents render: %v (%s)", err, out)
	}
	if !strings.Contains(out, "html_document_id") || !strings.Contains(out, "pdf_document_id") {
		t.Fatalf("expected both document ids in output, got: %s", out)
	}
}

func TestDocumentsRenderRejectsUnknownType(t *testing.T) {
	dbPath := newDocumentsCLITestDB(t)
	dataFile := filepath.Join(t.TempDir(), "data.json")
	if err := os.WriteFile(dataFile, []byte(`{}`), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := runDocumentsCmd(t, dbPath, "render", "--type", "nonsense", "--data-file", dataFile); err == nil {
		t.Fatal("expected error for unknown --type, got nil")
	}
}
