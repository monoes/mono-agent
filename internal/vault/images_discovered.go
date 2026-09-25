package vault

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
)

// RegisterDiscoveredImage records an image file found on disk under the
// user's project directory as a vault_images entry with source="discovered".
// The file is tracked in-place and is NEVER copied into the vault folder.
// Idempotent: returns the existing id and created=false if (profileID, path)
// is already tracked.
func RegisterDiscoveredImage(ctx context.Context, db *sql.DB, profileID, path, filename string, sizeBytes int64) (id string, created bool, err error) {
	if db == nil {
		return "", false, fmt.Errorf("vault.RegisterDiscoveredImage: db is nil")
	}
	if path == "" {
		return "", false, fmt.Errorf("vault.RegisterDiscoveredImage: path is empty")
	}
	if profileID == "" {
		profileID = "default"
	}
	if filename == "" {
		filename = filepath.Base(path)
	}

	if existingID, ok, lookupErr := imageIDByPath(ctx, db, profileID, path); lookupErr != nil {
		return "", false, fmt.Errorf("vault.RegisterDiscoveredImage: %w", lookupErr)
	} else if ok {
		return existingID, false, nil
	}

	conn, err := db.Conn(ctx)
	if err != nil {
		return "", false, fmt.Errorf("vault.RegisterDiscoveredImage: get conn: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return "", false, fmt.Errorf("vault.RegisterDiscoveredImage: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	var existingID string
	err = conn.QueryRowContext(ctx, `SELECT id FROM vault_images WHERE profile_id = ? AND path = ?`, profileID, path).Scan(&existingID)
	if err == nil {
		if _, rbErr := conn.ExecContext(ctx, "ROLLBACK"); rbErr == nil {
			committed = true
		}
		return existingID, false, nil
	}
	if err != sql.ErrNoRows {
		return "", false, fmt.Errorf("vault.RegisterDiscoveredImage: checking existing: %w", err)
	}

	var seq int
	if err := conn.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) + 1 FROM vault_images`).Scan(&seq); err != nil {
		return "", false, fmt.Errorf("vault.RegisterDiscoveredImage: get next seq: %w", err)
	}
	id = fmt.Sprintf("img-%03d", seq)

	_, err = conn.ExecContext(ctx, `
		INSERT INTO vault_images (id, seq, path, filename, size_bytes, source, profile_id, created_at)
		VALUES (?, ?, ?, ?, ?, 'discovered', ?, datetime('now'))`,
		id, seq, path, filename, sizeBytes, profileID,
	)
	if err != nil {
		return "", false, fmt.Errorf("vault.RegisterDiscoveredImage: insert: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return "", false, fmt.Errorf("vault.RegisterDiscoveredImage: commit: %w", err)
	}
	committed = true
	return id, true, nil
}

func imageIDByPath(ctx context.Context, db *sql.DB, profileID, path string) (id string, ok bool, err error) {
	err = db.QueryRowContext(ctx, `SELECT id FROM vault_images WHERE profile_id = ? AND path = ?`, profileID, path).Scan(&id)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return id, true, nil
}

// ReconcileDiscoveredImages registers a row for every image file in found not
// already tracked under profileID by path, refreshes size_bytes for ones
// that already are, and removes any "discovered"-sourced row whose path no
// longer appears in found (e.g. a deleted or moved project image).
//
// Only rows with source="discovered" are ever candidates for removal:
// manually uploaded, workflow-generated, or chat-generated images are never
// purged here.
func ReconcileDiscoveredImages(ctx context.Context, db *sql.DB, profileID string, found []DiscoveredFile) (added int, removed int, errs []error) {
	if profileID == "" {
		profileID = "default"
	}
	existing := make(map[string]int64, len(found))
	rows, err := db.QueryContext(ctx, `SELECT path, size_bytes FROM vault_images WHERE profile_id = ? AND source = 'discovered'`, profileID)
	if err != nil {
		return 0, 0, []error{fmt.Errorf("vault.ReconcileDiscoveredImages: loading existing: %w", err)}
	}
	for rows.Next() {
		var path string
		var size int64
		if scanErr := rows.Scan(&path, &size); scanErr != nil {
			rows.Close()
			return 0, 0, []error{fmt.Errorf("vault.ReconcileDiscoveredImages: scanning existing: %w", scanErr)}
		}
		existing[path] = size
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, []error{fmt.Errorf("vault.ReconcileDiscoveredImages: %w", err)}
	}

	foundPaths := make(map[string]bool, len(found))
	for _, f := range found {
		foundPaths[f.Path] = true
		size, tracked := existing[f.Path]
		if !tracked {
			_, created, regErr := RegisterDiscoveredImage(ctx, db, profileID, f.Path, f.Filename, f.SizeBytes)
			if regErr != nil {
				errs = append(errs, fmt.Errorf("registering %s: %w", f.Path, regErr))
				continue
			}
			if created {
				added++
			}
			continue
		}
		if size != f.SizeBytes {
			if _, updErr := db.ExecContext(ctx, `UPDATE vault_images SET size_bytes = ? WHERE profile_id = ? AND path = ?`, f.SizeBytes, profileID, f.Path); updErr != nil {
				errs = append(errs, fmt.Errorf("refreshing size for %s: %w", f.Path, updErr))
			}
		}
	}

	for path := range existing {
		if foundPaths[path] {
			continue
		}
		if _, delErr := db.ExecContext(ctx, `DELETE FROM vault_images WHERE profile_id = ? AND path = ? AND source = 'discovered'`, profileID, path); delErr != nil {
			errs = append(errs, fmt.Errorf("removing vanished %s: %w", path, delErr))
			continue
		}
		removed++
	}

	return added, removed, errs
}

// DeleteImage removes id's vault_images row, scoped to profileID.
// If the image's source is NOT "discovered", the underlying file on disk
// is also deleted. For source="discovered", the project file is untouched.
func DeleteImage(ctx context.Context, db *sql.DB, profileID, id string) error {
	if profileID == "" {
		profileID = "default"
	}
	var path, source string
	err := db.QueryRowContext(ctx,
		`SELECT path, source FROM vault_images WHERE id = ? AND profile_id = ?`, id, profileID,
	).Scan(&path, &source)
	if err == sql.ErrNoRows {
		return fmt.Errorf("vault.DeleteImage: id %q not found", id)
	}
	if err != nil {
		return fmt.Errorf("vault.DeleteImage: %w", err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM vault_images WHERE id = ? AND profile_id = ?`, id, profileID); err != nil {
		return fmt.Errorf("vault.DeleteImage: %w", err)
	}
	if source != "discovered" {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("vault.DeleteImage: remove file: %w", err)
		}
	}
	return nil
}
