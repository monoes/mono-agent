// Package personphoto saves people's profile photos locally. Platforms give
// a photo as a CDN URL that expires (Instagram, LinkedIn) or refuses
// hotlinking, so a stored URL soon shows nothing; Localize downloads the
// image into the profile's vault and points people.image_url at it.
package personphoto

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/monoes/mono-agent/internal/vault"
)

// Source is the vault_images.source of a saved profile photo.
const Source = "profile-photo"

const (
	maxBytes    = 10 << 20
	concurrency = 4
	userAgent   = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36"
)

var client = &http.Client{Timeout: 20 * time.Second}

// Ref names a person by platform and username (platform in upper case).
type Ref struct{ Platform, Username string }

type target struct{ id, url, oldImageID string }

// Localize downloads the remote photo of each person in refs (every person
// of the profile when refs is nil) and points the person at the saved copy.
// The original URL is kept in profile_details.photo_source_url. A person
// whose photo cannot be fetched keeps the remote URL; the failures are
// returned, the rest still saved. It returns how many photos were saved.
func Localize(ctx context.Context, db *sql.DB, profileID string, refs []Ref) (int, []error) {
	if db == nil {
		return 0, nil
	}
	targets, err := load(ctx, db, profileID, refs)
	if err != nil {
		return 0, []error{err}
	}
	ctx = vault.ContextWithProfileID(ctx, profileID)

	var (
		mu    sync.Mutex
		wg    sync.WaitGroup
		saved int
		errs  []error
		sem   = make(chan struct{}, concurrency)
	)
	for _, t := range targets {
		wg.Add(1)
		sem <- struct{}{}
		go func(t target) {
			defer wg.Done()
			defer func() { <-sem }()
			err := save(ctx, db, profileID, t)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, fmt.Errorf("personphoto: %s: %w", hostOf(t.url), err))
			} else {
				saved++
			}
		}(t)
	}
	wg.Wait()
	return saved, errs
}

func load(ctx context.Context, db *sql.DB, profileID string, refs []Ref) ([]target, error) {
	const cols = `SELECT id, image_url, COALESCE(CASE WHEN json_valid(profile_details) THEN json_extract(profile_details, '$.photo_image_id') END, '')
		FROM people WHERE profile_id = ? AND (image_url LIKE 'http://%' OR image_url LIKE 'https://%')`
	query, args := cols, []interface{}{profileID}
	if refs != nil {
		if len(refs) == 0 {
			return nil, nil
		}
		conds := make([]string, len(refs))
		for i, r := range refs {
			conds[i] = "(platform = ? AND platform_username = ?)"
			args = append(args, r.Platform, r.Username)
		}
		query += " AND (" + strings.Join(conds, " OR ") + ")"
	}
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("personphoto: listing people: %w", err)
	}
	defer rows.Close()
	var out []target
	for rows.Next() {
		var t target
		if err := rows.Scan(&t.id, &t.url, &t.oldImageID); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func save(ctx context.Context, db *sql.DB, profileID string, t target) error {
	path, err := download(ctx, t.url)
	if err != nil {
		return err
	}
	defer os.Remove(path)
	id, err := vault.Register(ctx, db, path, Source, "", "")
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `UPDATE people SET image_url = ?, profile_details = json_set(
			CASE WHEN json_valid(profile_details) THEN profile_details ELSE '{}' END,
			'$.photo_source_url', ?, '$.photo_image_id', ?)
		WHERE id = ?`, "/vault-image/"+id, t.url, id, t.id)
	if err != nil {
		_ = vault.DeleteImage(ctx, db, profileID, id)
		return err
	}
	if t.oldImageID != "" && t.oldImageID != id {
		_ = vault.DeleteImage(ctx, db, profileID, t.oldImageID) // the photo this one replaces
	}
	return nil
}

func hostOf(raw string) string {
	if u, err := neturl.Parse(raw); err == nil && u.Host != "" {
		return u.Host
	}
	return "photo"
}

// download fetches url into a temp file named for its image type.
func download(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "image/avif,image/webp,image/*,*/*;q=0.8")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return "", err
	}
	if len(body) > maxBytes {
		return "", errors.New("image over 10 MB")
	}
	ext, ok := map[string]string{
		"image/jpeg": ".jpg", "image/png": ".png", "image/gif": ".gif", "image/webp": ".webp",
	}[http.DetectContentType(body)]
	if !ok {
		return "", errors.New("not an image")
	}
	f, err := os.CreateTemp("", "person-photo-*"+ext)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.Write(body); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return filepath.Clean(f.Name()), nil
}
