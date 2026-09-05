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
	ID        string
	Path      string
	Filename  string
	SizeBytes int64
	Source    string
	CreatedAt string
}

// RegisterDocument copies src into the profile's vault (under a
// documents/ subdirectory of the same VaultDir used for images) and
// inserts a vault_documents row. Returns the new vault ID (e.g. "doc-001").
// Mirrors Register's structure exactly (same BEGIN IMMEDIATE seq-allocation
// pattern — see Register's doc comment in vault.go for why a deferred
// transaction would race two concurrent Registers onto the same seq).
func RegisterDocument(ctx context.Context, db *sql.DB, src, source string) (string, error) {
	if db == nil {
		return "", fmt.Errorf("vault.RegisterDocument: db is nil")
	}
	if src == "" {
		return "", fmt.Errorf("vault.RegisterDocument: src path is empty")
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

	_, err = conn.ExecContext(ctx, `
		INSERT INTO vault_documents (id, seq, path, filename, size_bytes, source, profile_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
		id, seq, destPath, filename, fi.Size(), source, profileID,
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
		`SELECT id, path, filename, size_bytes, source, created_at
		 FROM vault_documents WHERE profile_id = ? ORDER BY seq DESC`, profileID)
	if err != nil {
		return nil, fmt.Errorf("vault.ListDocuments: %w", err)
	}
	defer rows.Close()
	docs := []DocumentEntry{}
	for rows.Next() {
		var d DocumentEntry
		if err := rows.Scan(&d.ID, &d.Path, &d.Filename, &d.SizeBytes, &d.Source, &d.CreatedAt); err != nil {
			return nil, fmt.Errorf("vault.ListDocuments: scan: %w", err)
		}
		docs = append(docs, d)
	}
	return docs, rows.Err()
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
