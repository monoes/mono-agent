package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/monoes/mono-agent/internal/vault"
)

func writeTestDocFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "resume.txt")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

func TestGetProfileDocument_Found(t *testing.T) {
	// vault.RegisterDocument resolves its storage path via profiledir.Root,
	// which falls back to the real $HOME when there's no profiles.root_dir
	// override — without this, the test writes into the developer's actual
	// ~/.monoagent vault (same guard internal/vault/documents_test.go uses).
	t.Setenv("HOME", t.TempDir())
	a := newTestApp(t)
	ctx := vault.ContextWithDB(context.Background(), a.db)
	id, err := vault.RegisterDocument(ctx, a.db, writeTestDocFile(t, "resume content"), "upload")
	if err != nil {
		t.Fatalf("RegisterDocument: %v", err)
	}

	doc, err := a.GetProfileDocument(id)
	if err != nil {
		t.Fatalf("GetProfileDocument: %v", err)
	}
	if doc == nil || doc.ID != id || doc.Filename == "" {
		t.Fatalf("expected a matching document, got %+v", doc)
	}
}

func TestGetProfileDocument_NotFound(t *testing.T) {
	a := newTestApp(t)

	doc, err := a.GetProfileDocument("doc-999")
	if err == nil {
		t.Fatalf("expected an error for a missing document, got doc=%+v", doc)
	}
	if doc != nil {
		t.Fatalf("expected nil document alongside the error, got %+v", doc)
	}
}
