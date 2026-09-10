// internal/vault/documents_test.go
package vault_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/storage"
	"github.com/monoes/mono-agent/internal/vault"
)

func newTestDB(t *testing.T) *storage.Database {
	t.Helper()
	// vault.RegisterDocument resolves its storage path via profiledir.Root,
	// which falls back to the real $HOME when the test DB has no
	// profiles.root_dir override. Without this, every run of this test
	// writes real files into the developer's actual ~/.monoagent vault.
	t.Setenv("HOME", t.TempDir())
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

// TestComputeStaleFalseWhenBaselineIsNull is the regression test for the
// bug caught during design: a document indexed BEFORE the
// indexed_mtime/indexed_size_bytes columns existed (or one that predates
// this migration entirely) has NULL baselines, which must read as "cannot
// be stale," never as a zero-value mismatch against the real file's
// (mtime, size). Simulated here by marking a row indexed directly via a
// raw UPDATE (bypassing SetDocumentIndexed, which always writes real
// baselines on success) to reproduce exactly a pre-migration row's shape.
func TestComputeStaleFalseWhenBaselineIsNull(t *testing.T) {
	db := newTestDB(t)
	ctx := vault.ContextWithDB(context.Background(), db.DB)
	id, err := vault.RegisterDocument(ctx, db.DB, writeTestFile(t, "content"), "upload")
	if err != nil {
		t.Fatalf("RegisterDocument: %v", err)
	}
	if _, err := db.DB.ExecContext(ctx, `UPDATE vault_documents SET indexed = 1 WHERE id = ?`, id); err != nil {
		t.Fatalf("simulating a pre-migration indexed row: %v", err)
	}

	docs, err := vault.ListDocuments(ctx, db.DB, "default")
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if len(docs) != 1 || !docs[0].Indexed {
		t.Fatalf("expected the row to be Indexed, got %+v", docs)
	}
	if docs[0].Stale {
		t.Fatalf("expected Stale=false for a NULL baseline, got true: %+v", docs[0])
	}
}

func TestComputeStaleFalseWhenUnchangedSinceIndex(t *testing.T) {
	db := newTestDB(t)
	ctx := vault.ContextWithDB(context.Background(), db.DB)
	src := writeTestFile(t, "content")
	id, err := vault.RegisterDocument(ctx, db.DB, src, "upload")
	if err != nil {
		t.Fatalf("RegisterDocument: %v", err)
	}
	docs, err := vault.ListDocuments(ctx, db.DB, "default")
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	fi, err := os.Stat(docs[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.SetDocumentIndexed(ctx, db.DB, "default", id, true, "", fi.ModTime().UnixNano(), fi.Size()); err != nil {
		t.Fatalf("SetDocumentIndexed: %v", err)
	}

	docs, err = vault.ListDocuments(ctx, db.DB, "default")
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if docs[0].Stale {
		t.Fatalf("expected Stale=false immediately after a successful index, got true: %+v", docs[0])
	}
}

func TestComputeStaleTrueAfterContentChange(t *testing.T) {
	db := newTestDB(t)
	ctx := vault.ContextWithDB(context.Background(), db.DB)
	src := writeTestFile(t, "content")
	id, err := vault.RegisterDocument(ctx, db.DB, src, "upload")
	if err != nil {
		t.Fatalf("RegisterDocument: %v", err)
	}
	docs, err := vault.ListDocuments(ctx, db.DB, "default")
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	path := docs[0].Path
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.SetDocumentIndexed(ctx, db.DB, "default", id, true, "", fi.ModTime().UnixNano(), fi.Size()); err != nil {
		t.Fatalf("SetDocumentIndexed: %v", err)
	}

	// Change the file's content/size on disk, after it was indexed.
	if err := os.WriteFile(path, []byte("much longer content than before"), 0644); err != nil {
		t.Fatal(err)
	}

	docs, err = vault.ListDocuments(ctx, db.DB, "default")
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if !docs[0].Stale {
		t.Fatalf("expected Stale=true after the file changed on disk, got false: %+v", docs[0])
	}
}

func TestSetDocumentIndexedFailureDoesNotClearPriorSuccessBaseline(t *testing.T) {
	db := newTestDB(t)
	ctx := vault.ContextWithDB(context.Background(), db.DB)
	src := writeTestFile(t, "content")
	id, err := vault.RegisterDocument(ctx, db.DB, src, "upload")
	if err != nil {
		t.Fatalf("RegisterDocument: %v", err)
	}
	docs, err := vault.ListDocuments(ctx, db.DB, "default")
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	fi, err := os.Stat(docs[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.SetDocumentIndexed(ctx, db.DB, "default", id, true, "", fi.ModTime().UnixNano(), fi.Size()); err != nil {
		t.Fatalf("SetDocumentIndexed (success): %v", err)
	}

	// A later re-index attempt fails.
	if err := vault.SetDocumentIndexed(ctx, db.DB, "default", id, false, "knowledge_ingest: exit status 1", 0, 0); err != nil {
		t.Fatalf("SetDocumentIndexed (failure): %v", err)
	}

	docs, err = vault.ListDocuments(ctx, db.DB, "default")
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if !docs[0].Indexed {
		t.Fatalf("expected Indexed to remain true after a failed re-index, got false: %+v", docs[0])
	}
	if docs[0].IndexError == "" {
		t.Fatalf("expected the new IndexError to be recorded, got empty: %+v", docs[0])
	}
	if docs[0].Stale {
		t.Fatalf("expected Stale=false: the prior success's baseline (matching the unchanged file) must survive a failed re-index, got true: %+v", docs[0])
	}
}

// TestDeleteDocumentDoesNotRemoveFileForDiscoveredSource is the regression
// test for the other must-fix bug: a discovered document's path is the
// user's ORIGINAL file, never a vault copy, so deleting the row must never
// delete the file.
func TestDeleteDocumentDoesNotRemoveFileForDiscoveredSource(t *testing.T) {
	db := newTestDB(t)
	ctx := vault.ContextWithDB(context.Background(), db.DB)
	path := writeTestFile(t, "a file the app never copied")

	id, _, err := vault.RegisterDiscoveredDocument(ctx, db.DB, "default", path, filepath.Base(path), 123)
	if err != nil {
		t.Fatalf("RegisterDiscoveredDocument: %v", err)
	}

	if err := vault.DeleteDocument(ctx, db.DB, "default", id); err != nil {
		t.Fatalf("DeleteDocument: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected the original file to still exist after deleting a discovered row, got: %v", err)
	}
	docs, err := vault.ListDocuments(ctx, db.DB, "default")
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if len(docs) != 0 {
		t.Fatalf("expected the row to be gone, got %+v", docs)
	}
}

func TestDeleteDocumentStillRemovesFileForUploadedSource(t *testing.T) {
	db := newTestDB(t)
	ctx := vault.ContextWithDB(context.Background(), db.DB)
	id, err := vault.RegisterDocument(ctx, db.DB, writeTestFile(t, "content"), "upload")
	if err != nil {
		t.Fatalf("RegisterDocument: %v", err)
	}
	docs, err := vault.ListDocuments(ctx, db.DB, "default")
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	path := docs[0].Path

	if err := vault.DeleteDocument(ctx, db.DB, "default", id); err != nil {
		t.Fatalf("DeleteDocument: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected the vault copy to be removed for an uploaded document, stat err: %v", err)
	}
}

func TestRegisterDiscoveredDocumentCreatesRowWithoutCopying(t *testing.T) {
	db := newTestDB(t)
	ctx := vault.ContextWithDB(context.Background(), db.DB)
	path := writeTestFile(t, "original content")

	id, created, err := vault.RegisterDiscoveredDocument(ctx, db.DB, "default", path, filepath.Base(path), 17)
	if err != nil {
		t.Fatalf("RegisterDiscoveredDocument: %v", err)
	}
	if !created {
		t.Fatalf("expected created=true for a brand new path")
	}

	docs, err := vault.ListDocuments(ctx, db.DB, "default")
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if len(docs) != 1 || docs[0].ID != id {
		t.Fatalf("unexpected documents: %+v", docs)
	}
	if docs[0].Path != path {
		t.Fatalf("expected Path to equal the original path verbatim %q, got %q", path, docs[0].Path)
	}
	if docs[0].Source != "discovered" {
		t.Fatalf("expected source=discovered, got %q", docs[0].Source)
	}

	// No copy anywhere under the vault's documents/ dir.
	docsDir := filepath.Join(vault.VaultDir(db.DB, "default"), "documents")
	entries, _ := os.ReadDir(docsDir)
	if len(entries) != 0 {
		t.Fatalf("expected no files copied into the vault for a discovered document, found %d", len(entries))
	}
}

func TestRegisterDiscoveredDocumentIdempotentByPath(t *testing.T) {
	db := newTestDB(t)
	ctx := vault.ContextWithDB(context.Background(), db.DB)
	path := writeTestFile(t, "content")

	id1, created1, err := vault.RegisterDiscoveredDocument(ctx, db.DB, "default", path, filepath.Base(path), 7)
	if err != nil {
		t.Fatalf("RegisterDiscoveredDocument (1): %v", err)
	}
	if !created1 {
		t.Fatalf("expected created=true the first time")
	}

	id2, created2, err := vault.RegisterDiscoveredDocument(ctx, db.DB, "default", path, filepath.Base(path), 7)
	if err != nil {
		t.Fatalf("RegisterDiscoveredDocument (2): %v", err)
	}
	if created2 {
		t.Fatalf("expected created=false the second time (already tracked)")
	}
	if id1 != id2 {
		t.Fatalf("expected the same id both times, got %q then %q", id1, id2)
	}

	docs, err := vault.ListDocuments(ctx, db.DB, "default")
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected exactly one row, got %d", len(docs))
	}
}

func TestRegisterDiscoveredDocumentPathHasProfileRootPrefix(t *testing.T) {
	db := newTestDB(t)
	ctx := vault.ContextWithDB(context.Background(), db.DB)
	root := vault.VaultDir(db.DB, "default") // under the profile root
	path := filepath.Join(filepath.Dir(root), "resume.md")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}

	_, _, err := vault.RegisterDiscoveredDocument(ctx, db.DB, "default", path, "resume.md", 7)
	if err != nil {
		t.Fatalf("RegisterDiscoveredDocument: %v", err)
	}
	docs, err := vault.ListDocuments(ctx, db.DB, "default")
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if !strings.HasPrefix(docs[0].Path, filepath.Dir(root)) {
		t.Fatalf("expected stored path %q to have the profile root %q as a prefix", docs[0].Path, filepath.Dir(root))
	}
}

func TestReconcileDiscoveredDocumentsSkipsAlreadyTrackedPaths(t *testing.T) {
	db := newTestDB(t)
	ctx := vault.ContextWithDB(context.Background(), db.DB)
	id, err := vault.RegisterDocument(ctx, db.DB, writeTestFile(t, "content"), "upload")
	if err != nil {
		t.Fatalf("RegisterDocument: %v", err)
	}
	docs, err := vault.ListDocuments(ctx, db.DB, "default")
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	uploadedPath := docs[0].Path

	added, removed, errs := vault.ReconcileDiscoveredDocuments(ctx, db.DB, "default", []vault.DiscoveredFile{
		{Path: uploadedPath, Filename: filepath.Base(uploadedPath), SizeBytes: docs[0].SizeBytes},
	})
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if added != 0 {
		t.Fatalf("expected 0 added for an already-tracked path, got %d", added)
	}
	if removed != 0 {
		t.Fatalf("expected 0 removed, got %d", removed)
	}

	docs, err = vault.ListDocuments(ctx, db.DB, "default")
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if len(docs) != 1 || docs[0].ID != id {
		t.Fatalf("expected no duplicate row, got %+v", docs)
	}
}

func TestReconcileDiscoveredDocumentsAddsNewFiles(t *testing.T) {
	db := newTestDB(t)
	ctx := vault.ContextWithDB(context.Background(), db.DB)
	path := writeTestFile(t, "brand new")

	added, removed, errs := vault.ReconcileDiscoveredDocuments(ctx, db.DB, "default", []vault.DiscoveredFile{
		{Path: path, Filename: filepath.Base(path), SizeBytes: 10},
	})
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if added != 1 {
		t.Fatalf("expected 1 added, got %d", added)
	}
	if removed != 0 {
		t.Fatalf("expected 0 removed, got %d", removed)
	}

	docs, err := vault.ListDocuments(ctx, db.DB, "default")
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if len(docs) != 1 || docs[0].Source != "discovered" {
		t.Fatalf("unexpected documents: %+v", docs)
	}
}

func TestReconcileDiscoveredDocumentsRefreshesSize(t *testing.T) {
	db := newTestDB(t)
	ctx := vault.ContextWithDB(context.Background(), db.DB)
	path := writeTestFile(t, "content")

	if _, _, errs := vault.ReconcileDiscoveredDocuments(ctx, db.DB, "default", []vault.DiscoveredFile{
		{Path: path, Filename: filepath.Base(path), SizeBytes: 10},
	}); len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}

	if _, _, errs := vault.ReconcileDiscoveredDocuments(ctx, db.DB, "default", []vault.DiscoveredFile{
		{Path: path, Filename: filepath.Base(path), SizeBytes: 99},
	}); len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}

	docs, err := vault.ListDocuments(ctx, db.DB, "default")
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if len(docs) != 1 || docs[0].SizeBytes != 99 {
		t.Fatalf("expected size_bytes to refresh to 99, got %+v", docs)
	}
}

// TestReconcileDiscoveredDocumentsRemovesVanishedDiscoveredFile is the
// regression test for the gap that let ~1450 stale dot-folder rows survive
// indefinitely in a real install: a "discovered" row whose path no longer
// appears in a fresh (full-snapshot) scan -- because the file was deleted,
// moved, or is now excluded by docscan.Scan's own rules (e.g. a dot-folder
// docscan.isDotDir now excludes) -- must be purged, not left forever.
func TestReconcileDiscoveredDocumentsRemovesVanishedDiscoveredFile(t *testing.T) {
	db := newTestDB(t)
	ctx := vault.ContextWithDB(context.Background(), db.DB)
	path := writeTestFile(t, "will vanish")

	if _, _, errs := vault.ReconcileDiscoveredDocuments(ctx, db.DB, "default", []vault.DiscoveredFile{
		{Path: path, Filename: filepath.Base(path), SizeBytes: 11},
	}); len(errs) != 0 {
		t.Fatalf("unexpected errors on initial discovery: %v", errs)
	}
	docs, err := vault.ListDocuments(ctx, db.DB, "default")
	if err != nil || len(docs) != 1 {
		t.Fatalf("expected 1 discovered doc before reconcile, got %+v (err=%v)", docs, err)
	}

	// Reconcile again with an empty snapshot -- as docscan.Watcher always
	// passes the FULL current state, an empty found here means the file
	// genuinely vanished (or is now excluded), not a partial/delta update.
	added, removed, errs := vault.ReconcileDiscoveredDocuments(ctx, db.DB, "default", []vault.DiscoveredFile{})
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if added != 0 {
		t.Fatalf("expected 0 added, got %d", added)
	}
	if removed != 1 {
		t.Fatalf("expected 1 removed, got %d", removed)
	}

	docs, err = vault.ListDocuments(ctx, db.DB, "default")
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if len(docs) != 0 {
		t.Fatalf("expected the vanished discovered doc to be purged, got %+v", docs)
	}
}

// TestReconcileDiscoveredDocumentsNeverRemovesNonDiscoveredSource ensures
// the purge is scoped to source='discovered' only -- a manually
// uploaded/registered document has a different lifecycle (see
// DeleteDocument's own source check) and must never be silently deleted
// just because it doesn't happen to appear in a docscan snapshot.
func TestReconcileDiscoveredDocumentsNeverRemovesNonDiscoveredSource(t *testing.T) {
	db := newTestDB(t)
	ctx := vault.ContextWithDB(context.Background(), db.DB)
	id, err := vault.RegisterDocument(ctx, db.DB, writeTestFile(t, "uploaded"), "upload")
	if err != nil {
		t.Fatalf("RegisterDocument: %v", err)
	}

	_, removed, errs := vault.ReconcileDiscoveredDocuments(ctx, db.DB, "default", []vault.DiscoveredFile{})
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if removed != 0 {
		t.Fatalf("expected 0 removed (uploaded doc must survive), got %d", removed)
	}

	docs, err := vault.ListDocuments(ctx, db.DB, "default")
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if len(docs) != 1 || docs[0].ID != id {
		t.Fatalf("expected the uploaded doc to survive reconciliation, got %+v", docs)
	}
}
