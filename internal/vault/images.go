// internal/vault/images.go
package vault

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
)

// ErrImageNotFound is returned when no vault_images row has the given id
// in the given profile.
var ErrImageNotFound = errors.New("vault image not found")

// ImageEntry is one row from vault_images, in the JSON shape the desktop
// app's image vault has always used (URL is the app's asset route for the
// file, derived from Filename).
type ImageEntry struct {
	ID          string `json:"id"`
	Seq         int    `json:"seq"`
	Path        string `json:"path"`
	Filename    string `json:"filename"`
	SizeBytes   int64  `json:"size_bytes"`
	Source      string `json:"source"`
	WorkflowID  string `json:"workflow_id"`
	ExecutionID string `json:"execution_id"`
	Label       string `json:"label"`
	CreatedAt   string `json:"created_at"`
	URL         string `json:"url"`
}

// ImageStats is the count and total size of a profile's vault images.
type ImageStats struct {
	Count      int   `json:"count"`
	TotalBytes int64 `json:"total_bytes"`
}

// imageColumns is the column list the image queries select, in the order
// scanImage reads them.
const imageColumns = `id, seq, path, filename, size_bytes, source,
	COALESCE(workflow_id,''), COALESCE(execution_id,''), COALESCE(label,''), created_at`

func scanImage(scan func(dest ...any) error) (ImageEntry, error) {
	var im ImageEntry
	if err := scan(&im.ID, &im.Seq, &im.Path, &im.Filename, &im.SizeBytes, &im.Source,
		&im.WorkflowID, &im.ExecutionID, &im.Label, &im.CreatedAt); err != nil {
		return im, err
	}
	im.URL = "/vault-image/" + im.Filename
	return im, nil
}

func queryImages(ctx context.Context, db *sql.DB, query string, args ...any) ([]ImageEntry, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ImageEntry{}
	for rows.Next() {
		im, err := scanImage(rows.Scan)
		if err != nil {
			continue
		}
		out = append(out, im)
	}
	return out, rows.Err()
}

// ListImages returns a profile's newest limit images, newest first.
func ListImages(ctx context.Context, db *sql.DB, profileID string, limit int) ([]ImageEntry, error) {
	return queryImages(ctx, db, `SELECT `+imageColumns+`
		FROM vault_images WHERE profile_id = ? ORDER BY seq DESC LIMIT ?`, profileID, limit)
}

// SearchImages returns a profile's images whose label, filename, source or
// workflow id contains query (a literal substring: % and _ match
// themselves), newest first. An empty query matches every image.
func SearchImages(ctx context.Context, db *sql.DB, profileID, query string, limit int) ([]ImageEntry, error) {
	escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(query)
	q := "%" + escaped + "%"
	return queryImages(ctx, db, `SELECT `+imageColumns+`
		FROM vault_images
		WHERE profile_id = ? AND (label LIKE ? ESCAPE '\' OR filename LIKE ? ESCAPE '\' OR source LIKE ? ESCAPE '\' OR workflow_id LIKE ? ESCAPE '\')
		ORDER BY seq DESC LIMIT ?`, profileID, q, q, q, q, limit)
}

// GetImage returns one of a profile's images, or ErrImageNotFound.
func GetImage(ctx context.Context, db *sql.DB, profileID, id string) (*ImageEntry, error) {
	im, err := scanImage(db.QueryRowContext(ctx, `SELECT `+imageColumns+`
		FROM vault_images WHERE id = ? AND profile_id = ?`, id, profileID).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: %q", ErrImageNotFound, id)
	}
	if err != nil {
		return nil, err
	}
	return &im, nil
}

// SetImageLabel sets an image's label; an empty label clears it (NULL).
func SetImageLabel(ctx context.Context, db *sql.DB, profileID, id, label string) error {
	var value any
	if label != "" {
		value = label
	}
	res, err := db.ExecContext(ctx, `UPDATE vault_images SET label = ? WHERE id = ? AND profile_id = ?`, value, id, profileID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: %q", ErrImageNotFound, id)
	}
	return nil
}

// DeleteImage removes an image's row, then its file (best-effort: a file
// that is already gone does not fail the delete).
func DeleteImage(ctx context.Context, db *sql.DB, profileID, id string) error {
	im, err := GetImage(ctx, db, profileID, id)
	if err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM vault_images WHERE id = ? AND profile_id = ?`, id, profileID); err != nil {
		return fmt.Errorf("delete record: %w", err)
	}
	_ = os.Remove(im.Path)
	return nil
}

// GetImageStats counts a profile's images and sums their sizes.
func GetImageStats(ctx context.Context, db *sql.DB, profileID string) (ImageStats, error) {
	var st ImageStats
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(size_bytes),0) FROM vault_images WHERE profile_id = ?`, profileID).
		Scan(&st.Count, &st.TotalBytes)
	return st, err
}
