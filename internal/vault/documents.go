// internal/vault/documents.go
package vault

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
)

// DocumentEntry is one row from vault_documents.
type DocumentEntry struct {
	ID            string
	Path          string
	Filename      string
	SizeBytes     int64
	Source        string
	ApplicationID string
	CreatedAt     string
	Indexed       bool
	IndexError    string
}

// RegisterDocument copies src into the profile's vault (under a
// documents/ subdirectory of the same VaultDir used for images) and
// inserts a vault_documents row. Returns the new vault ID (e.g. "doc-001").
// applicationID is optional (variadic, matching internal/connections.Store
// .Get's ...string convention) — pass one string to link the document to
// a job/tender application, or omit it for a general profile document.
// Mirrors Register's structure exactly (same BEGIN IMMEDIATE seq-allocation
// pattern — see Register's doc comment in vault.go for why a deferred
// transaction would race two concurrent Registers onto the same seq).
func RegisterDocument(ctx context.Context, db *sql.DB, src, source string, applicationID ...string) (string, error) {
	if db == nil {
		return "", fmt.Errorf("vault.RegisterDocument: db is nil")
	}
	if src == "" {
		return "", fmt.Errorf("vault.RegisterDocument: src path is empty")
	}
	var appID string
	if len(applicationID) > 0 {
		appID = applicationID[0]
	}
	absSrc, err := filepath.Abs(src)
	if err != nil {
		return "", fmt.Errorf("vault.RegisterDocument: invalid src path: %w", err)
	}

	profileID := ProfileIDFromContext(ctx)
	docsDir := filepath.Join(VaultDir(db, profileID), "documents")
	if err := os.MkdirAll(docsDir, 0700); err != nil {
		return "", fmt.Errorf("vault.RegisterDocument: ensure documents dir: %w", err)
	}

	conn, err := db.Conn(ctx)
	if err != nil {
		return "", fmt.Errorf("vault.RegisterDocument: get conn: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return "", fmt.Errorf("vault.RegisterDocument: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	var seq int
	if err := conn.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) + 1 FROM vault_documents`).Scan(&seq); err != nil {
		return "", fmt.Errorf("vault.RegisterDocument: get next seq: %w", err)
	}

	id := fmt.Sprintf("doc-%03d", seq)
	filename := filepath.Base(absSrc)
	destPath := filepath.Join(docsDir, fmt.Sprintf("%s%s", id, filepath.Ext(filename)))

	if err := copyFile(absSrc, destPath); err != nil {
		_ = os.Remove(destPath)
		return "", fmt.Errorf("vault.RegisterDocument: copy file: %w", err)
	}
	fi, err := os.Stat(destPath)
	if err != nil {
		_ = os.Remove(destPath)
		return "", fmt.Errorf("vault.RegisterDocument: stat dest: %w", err)
	}

	nullStr := func(s string) interface{} {
		if s == "" {
			return nil
		}
		return s
	}

	_, err = conn.ExecContext(ctx, `
		INSERT INTO vault_documents (id, seq, path, filename, size_bytes, source, application_id, profile_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
		id, seq, destPath, filename, fi.Size(), source, nullStr(appID), profileID,
	)
	if err != nil {
		os.Remove(destPath)
		return "", fmt.Errorf("vault.RegisterDocument: insert: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		os.Remove(destPath)
		return "", fmt.Errorf("vault.RegisterDocument: commit: %w", err)
	}
	committed = true
	return id, nil
}

// ListDocuments returns profileID's uploaded documents, newest first.
func ListDocuments(ctx context.Context, db *sql.DB, profileID string) ([]DocumentEntry, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, path, filename, size_bytes, source, COALESCE(application_id, ''), created_at, indexed, COALESCE(index_error, '')
		 FROM vault_documents WHERE profile_id = ? ORDER BY seq DESC`, profileID)
	if err != nil {
		return nil, fmt.Errorf("vault.ListDocuments: %w", err)
	}
	defer rows.Close()
	docs := []DocumentEntry{}
	for rows.Next() {
		var d DocumentEntry
		if err := rows.Scan(&d.ID, &d.Path, &d.Filename, &d.SizeBytes, &d.Source, &d.ApplicationID, &d.CreatedAt, &d.Indexed, &d.IndexError); err != nil {
			return nil, fmt.Errorf("vault.ListDocuments: scan: %w", err)
		}
		docs = append(docs, d)
	}
	return docs, rows.Err()
}

// SetDocumentIndexed records the outcome of a knowledge_ingest attempt for
// id, scoped to profileID. indexErr should be empty on success.
func SetDocumentIndexed(ctx context.Context, db *sql.DB, profileID, id string, indexed bool, indexErr string) error {
	var errVal interface{}
	if indexErr != "" {
		errVal = indexErr
	}
	res, err := db.ExecContext(ctx,
		`UPDATE vault_documents SET indexed = ?, index_error = ? WHERE id = ? AND profile_id = ?`,
		indexed, errVal, id, profileID,
	)
	if err != nil {
		return fmt.Errorf("vault.SetDocumentIndexed: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("vault.SetDocumentIndexed: rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("vault.SetDocumentIndexed: id %q not found", id)
	}
	return nil
}

// DeleteDocument removes id's vault_documents row and its file, scoped to
// profileID. Returns an error if the row does not exist.
func DeleteDocument(ctx context.Context, db *sql.DB, profileID, id string) error {
	var path string
	err := db.QueryRowContext(ctx,
		`SELECT path FROM vault_documents WHERE id = ? AND profile_id = ?`, id, profileID,
	).Scan(&path)
	if err == sql.ErrNoRows {
		return fmt.Errorf("vault.DeleteDocument: id %q not found", id)
	}
	if err != nil {
		return fmt.Errorf("vault.DeleteDocument: %w", err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM vault_documents WHERE id = ? AND profile_id = ?`, id, profileID); err != nil {
		return fmt.Errorf("vault.DeleteDocument: %w", err)
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "warning: document %s deleted from index but its file could not be removed: %v\n", id, err)
	}
	return nil
}
