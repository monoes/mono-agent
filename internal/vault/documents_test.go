// internal/vault/documents_test.go
package vault_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/monoes/mono-agent/internal/storage"
	"github.com/monoes/mono-agent/internal/vault"
)

func newTestDB(t *testing.T) *storage.Database {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "vault-documents-test.db")
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

func writeTestFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "resume.txt")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

func TestRegisterDocumentRoundTrip(t *testing.T) {
	db := newTestDB(t)
	ctx := vault.ContextWithDB(context.Background(), db.DB)
	src := writeTestFile(t, "Experienced backend engineer.")

	id, err := vault.RegisterDocument(ctx, db.DB, src, "upload")
	if err != nil {
		t.Fatalf("RegisterDocument: %v", err)
	}
	if id != "doc-001" {
		t.Fatalf("expected first document id doc-001, got %q", id)
	}

	docs, err := vault.ListDocuments(ctx, db.DB, "default")
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if len(docs) != 1 || docs[0].ID != id || docs[0].Filename != "resume.txt" {
		t.Fatalf("unexpected documents list: %+v", docs)
	}
	if _, err := os.Stat(docs[0].Path); err != nil {
		t.Fatalf("expected copied file to exist at %q: %v", docs[0].Path, err)
	}
}

func TestRegisterDocumentIncrementingSeq(t *testing.T) {
	db := newTestDB(t)
	ctx := vault.ContextWithDB(context.Background(), db.DB)

	id1, err := vault.RegisterDocument(ctx, db.DB, writeTestFile(t, "one"), "upload")
	if err != nil {
		t.Fatalf("RegisterDocument 1: %v", err)
	}
	id2, err := vault.RegisterDocument(ctx, db.DB, writeTestFile(t, "two"), "upload")
	if err != nil {
		t.Fatalf("RegisterDocument 2: %v", err)
	}
	if id1 == id2 {
		t.Fatalf("expected distinct ids, got %q twice", id1)
	}
}

func TestDeleteDocument(t *testing.T) {
	db := newTestDB(t)
	ctx := vault.ContextWithDB(context.Background(), db.DB)
	id, err := vault.RegisterDocument(ctx, db.DB, writeTestFile(t, "content"), "upload")
	if err != nil {
		t.Fatalf("RegisterDocument: %v", err)
	}

	if err := vault.DeleteDocument(ctx, db.DB, "default", id); err != nil {
		t.Fatalf("DeleteDocument: %v", err)
	}
	docs, err := vault.ListDocuments(ctx, db.DB, "default")
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if len(docs) != 0 {
		t.Fatalf("expected no documents after delete, got %d", len(docs))
	}
}

func TestListDocumentsScopedToProfile(t *testing.T) {
	db := newTestDB(t)
	ctx := vault.ContextWithDB(context.Background(), db.DB)
	if _, err := vault.RegisterDocument(ctx, db.DB, writeTestFile(t, "content"), "upload"); err != nil {
		t.Fatalf("RegisterDocument: %v", err)
	}
	docs, err := vault.ListDocuments(ctx, db.DB, "other-profile")
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if len(docs) != 0 {
		t.Fatalf("expected no documents for other-profile, got %d", len(docs))
	}
}

func TestRegisterDocumentWithApplicationID(t *testing.T) {
	db := newTestDB(t)
	ctx := vault.ContextWithDB(context.Background(), db.DB)

	id, err := vault.RegisterDocument(ctx, db.DB, writeTestFile(t, "cv content"), "generated", "app-123")
	if err != nil {
		t.Fatalf("RegisterDocument: %v", err)
	}
	docs, err := vault.ListDocuments(ctx, db.DB, "default")
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if len(docs) != 1 || docs[0].ID != id || docs[0].ApplicationID != "app-123" {
		t.Fatalf("expected application_id to round-trip, got %+v", docs)
	}
}

func TestRegisterDocumentWithoutApplicationIDStaysEmpty(t *testing.T) {
	db := newTestDB(t)
	ctx := vault.ContextWithDB(context.Background(), db.DB)

	if _, err := vault.RegisterDocument(ctx, db.DB, writeTestFile(t, "content"), "upload"); err != nil {
		t.Fatalf("RegisterDocument: %v", err)
	}
	docs, err := vault.ListDocuments(ctx, db.DB, "default")
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if len(docs) != 1 || docs[0].ApplicationID != "" {
		t.Fatalf("expected empty ApplicationID for a call with none supplied, got %+v", docs)
	}
}
