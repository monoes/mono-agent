package vault

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/profiledir"
)

type ctxKey struct{}

// ContextWithDB returns a new context carrying db for vault operations.
func ContextWithDB(ctx context.Context, db *sql.DB) context.Context {
	return context.WithValue(ctx, ctxKey{}, db)
}

// DBFromContext retrieves the *sql.DB stored by ContextWithDB. Returns nil if absent.
func DBFromContext(ctx context.Context) *sql.DB {
	db, _ := ctx.Value(ctxKey{}).(*sql.DB)
	return db
}

type execCtxKey struct{}

type execIDs struct {
	WorkflowID  string
	ExecutionID string
}

// ContextWithExecIDs stores workflow and execution IDs in context for vault registration.
func ContextWithExecIDs(ctx context.Context, workflowID, executionID string) context.Context {
	return context.WithValue(ctx, execCtxKey{}, execIDs{workflowID, executionID})
}

// ExecIDsFromContext retrieves the workflow and execution IDs stored by ContextWithExecIDs.
func ExecIDsFromContext(ctx context.Context) (workflowID, executionID string) {
	if v, ok := ctx.Value(execCtxKey{}).(execIDs); ok {
		return v.WorkflowID, v.ExecutionID
	}
	return "", ""
}

type profileCtxKey struct{}

// ContextWithProfileID stores a profile ID in context for vault registration.
func ContextWithProfileID(ctx context.Context, profileID string) context.Context {
	return context.WithValue(ctx, profileCtxKey{}, profileID)
}

// ProfileIDFromContext retrieves the profile ID stored by ContextWithProfileID.
// Returns "default" if absent.
func ProfileIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(profileCtxKey{}).(string); ok && v != "" {
		return v
	}
	return "default"
}

// VaultDir returns the absolute path of a profile's vault directory —
// either its chosen folder or the default
// ~/.monoagent/profiles/<profileID>/vault/.
func VaultDir(db *sql.DB, profileID string) string {
	return profiledir.VaultDir(db, profileID)
}

// EnsureVaultDir creates a profile's vault directory if it does not exist.
func EnsureVaultDir(db *sql.DB, profileID string) error {
	return os.MkdirAll(VaultDir(db, profileID), 0700)
}

// Register copies the file at src into the vault, inserts a DB row, and
// returns the new vault ID (e.g. "img-001").
// source should be "gemini", "upload", "huggingface", etc.
// workflowID and executionID may be empty strings.
func Register(ctx context.Context, db *sql.DB, src, source, workflowID, executionID string) (string, error) {
	if db == nil {
		return "", fmt.Errorf("vault.Register: db is nil")
	}
	if src == "" {
		return "", fmt.Errorf("vault.Register: src path is empty")
	}
	absSrc, err := filepath.Abs(src)
	if err != nil {
		return "", fmt.Errorf("vault.Register: invalid src path: %w", err)
	}
	src = absSrc

	profileID := ProfileIDFromContext(ctx)
	vaultDir := VaultDir(db, profileID)
	if strings.HasPrefix(absSrc, vaultDir+string(os.PathSeparator)) || absSrc == vaultDir {
		return "", fmt.Errorf("vault.Register: src must not be inside the vault directory")
	}

	if err := EnsureVaultDir(db, profileID); err != nil {
		return "", fmt.Errorf("vault.Register: ensure vault dir: %w", err)
	}

	// Take a dedicated connection and open an IMMEDIATE transaction so the
	// write lock is acquired up front. database/sql's default BeginTx starts a
	// DEFERRED (reader) transaction under SQLite, which lets two concurrent
	// Registers both read the same MAX(seq) and race on the same destPath; the
	// loser's cleanup would then delete the winner's committed vault file.
	// BEGIN IMMEDIATE serializes seq allocation across connections/processes
	// (waiting up to busy_timeout for a concurrent writer).
	conn, err := db.Conn(ctx)
	if err != nil {
		return "", fmt.Errorf("vault.Register: get conn: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return "", fmt.Errorf("vault.Register: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			// Use a fresh context so rollback still runs if ctx was cancelled.
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	var seq int
	if err := conn.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) + 1 FROM vault_images`).Scan(&seq); err != nil {
		return "", fmt.Errorf("vault.Register: get next seq: %w", err)
	}

	id := fmt.Sprintf("img-%03d", seq)
	ext := filepath.Ext(src)
	if ext == "" {
		ext = ".png"
	}
	destFilename := id + ext
	destPath := filepath.Join(vaultDir, destFilename)

	// Copy source file to vault.
	if err := copyFile(src, destPath); err != nil {
		_ = os.Remove(destPath) // best-effort cleanup of partial file
		return "", fmt.Errorf("vault.Register: copy file: %w", err)
	}

	// Get file size.
	fi, err := os.Stat(destPath)
	if err != nil {
		_ = os.Remove(destPath) // best-effort cleanup
		return "", fmt.Errorf("vault.Register: stat dest: %w", err)
	}

	// Nullable string helpers.
	nullStr := func(s string) interface{} {
		if s == "" {
			return nil
		}
		return s
	}

	_, err = conn.ExecContext(ctx, `
		INSERT INTO vault_images (id, seq, path, filename, size_bytes, source, workflow_id, execution_id, profile_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, seq, destPath, destFilename, fi.Size(), source,
		nullStr(workflowID), nullStr(executionID), profileID,
	)
	if err != nil {
		os.Remove(destPath) // best-effort cleanup
		return "", fmt.Errorf("vault.Register: insert: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		os.Remove(destPath)
		return "", fmt.Errorf("vault.Register: commit: %w", err)
	}
	committed = true

	go func() {
		desc := "source: " + source
		if workflowID != "" {
			desc += ", workflow: " + workflowID
		}
		if executionID != "" {
			desc += ", execution: " + executionID
		}
		node := monomind.KGNode{Name: id, Type: "file", Description: desc}
		_ = monomind.SyncToKnowledgeGraph(context.Background(), db, profileID, []monomind.KGNode{node}, nil, "vault:"+id)
	}()

	return id, nil
}

// legacyVaultDir is the single flat directory every profile's files used to
// share before per-profile folders existed. Kept only for MigrateVaultFiles.
func legacyVaultDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.Getenv("HOME")
	}
	return filepath.Join(home, ".monoagent", "vault")
}

// MigrateVaultFiles moves one profile's files out of the old shared flat
// vault directory into its own vault/ folder, updating each vault_images
// row's stored path to match. Idempotent: rows already pointing outside the
// legacy directory (i.e. already migrated, or never lived there) are left
// untouched. Thin wrapper over MoveFiles — kept as its own name since this
// specific "off the old shared dir" case runs unconditionally on every
// startup, same as this file's sibling migrations.
func MigrateVaultFiles(ctx context.Context, db *sql.DB, profileID string) (moved int, errs []error) {
	return MoveFiles(ctx, db, profileID, legacyVaultDir(), VaultDir(db, profileID))
}

// MoveFiles moves a profile's vault_images files from fromDir to toDir,
// updating each row's stored path to match. Idempotent: rows whose path
// isn't inside fromDir (already moved, or never lived there) are left
// untouched — so re-running after a partial failure only retries what
// didn't move yet. A failure on one row is reported via the returned
// per-row error list rather than aborting the rest.
func MoveFiles(ctx context.Context, db *sql.DB, profileID, fromDir, toDir string) (moved int, errs []error) {
	if err := os.MkdirAll(toDir, 0700); err != nil {
		return 0, []error{fmt.Errorf("vault.MoveFiles: ensure dest dir: %w", err)}
	}

	rows, err := db.QueryContext(ctx,
		`SELECT id, path, filename FROM vault_images WHERE COALESCE(profile_id,'default') = ?`, profileID)
	if err != nil {
		return 0, []error{fmt.Errorf("vault.MoveFiles: query rows: %w", err)}
	}
	type row struct{ id, path, filename string }
	var toMove []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.path, &r.filename); err == nil {
			toMove = append(toMove, r)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		errs = append(errs, fmt.Errorf("vault.MoveFiles: iterate rows: %w", err))
	}

	for _, r := range toMove {
		if !strings.HasPrefix(r.path, fromDir+string(os.PathSeparator)) {
			continue // already moved, or never lived in fromDir
		}
		destPath := filepath.Join(toDir, r.filename)
		if err := os.Rename(r.path, destPath); err != nil {
			errs = append(errs, fmt.Errorf("vault.MoveFiles: move %s: %w", r.id, err))
			continue
		}
		if _, err := db.ExecContext(ctx, `UPDATE vault_images SET path = ? WHERE id = ?`, destPath, r.id); err != nil {
			errs = append(errs, fmt.Errorf("vault.MoveFiles: update path for %s: %w", r.id, err))
			continue
		}
		moved++
	}
	return moved, errs
}

// Resolve turns "@img-001" into the absolute file path stored in the DB.
// Returns an error if the image is not found.
func Resolve(ctx context.Context, db *sql.DB, ref string) (string, error) {
	if !strings.HasPrefix(ref, "@") {
		return ref, nil
	}
	id := strings.TrimPrefix(ref, "@")
	profileID := ProfileIDFromContext(ctx)
	var path string
	err := db.QueryRowContext(ctx,
		`SELECT path FROM vault_images WHERE id = ? AND COALESCE(profile_id,'default') = ?`,
		id, profileID).Scan(&path)
	if err == sql.ErrNoRows {
		return ref, fmt.Errorf("vault.Resolve: image %q not found", id)
	}
	if err != nil {
		return ref, fmt.Errorf("vault.Resolve: %w", err)
	}
	return path, nil
}

// ResolveConfig walks a config map and replaces any string value that starts
// with "@img-" with its absolute file path from the vault.
// Keys with missing images are left as-is (the ref string) and a warning is logged to stderr.
func ResolveConfig(ctx context.Context, db *sql.DB, config map[string]interface{}) error {
	if db == nil {
		return nil
	}
	for k, v := range config {
		s, ok := v.(string)
		if !ok {
			continue
		}
		if !strings.HasPrefix(s, "@img-") {
			continue
		}
		resolved, err := Resolve(ctx, db, s)
		if err != nil {
			// Non-fatal: leave original ref, emit warning.
			fmt.Fprintf(os.Stderr, "vault: warning: %v\n", err)
			continue
		}
		config[k] = resolved
	}
	return nil
}

func copyFile(src, dst string) (retErr error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() {
		cerr := out.Close()
		if retErr == nil {
			retErr = cerr
		}
	}()
	_, err = io.Copy(out, in)
	return err
}
