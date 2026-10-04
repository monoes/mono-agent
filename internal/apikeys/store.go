package apikeys

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Store reads and writes the api_keys table.
type Store struct {
	db  *sql.DB
	now func() time.Time

	mu      sync.Mutex
	touched map[string]time.Time // key id -> when last_used_at was last written
}

// NewStore returns a Store over a migrated database.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db, now: time.Now, touched: map[string]time.Time{}}
}

const selectKey = `SELECT id, profile_id, name, prefix, context, created_at, last_used_at, revoked_at FROM api_keys`

type scanner interface{ Scan(dest ...any) error }

func scanKey(row scanner) (Key, error) {
	var (
		k                   Key
		ctx                 int
		created             string
		lastUsed, revokedAt sql.NullString
	)
	if err := row.Scan(&k.ID, &k.ProfileID, &k.Name, &k.Prefix, &ctx, &created, &lastUsed, &revokedAt); err != nil {
		return Key{}, err
	}
	k.Context = ctx != 0
	k.CreatedAt, _ = time.Parse(timeFmt, created)
	if lastUsed.Valid {
		t, _ := time.Parse(timeFmt, lastUsed.String)
		k.LastUsedAt = &t
	}
	if revokedAt.Valid {
		t, _ := time.Parse(timeFmt, revokedAt.String)
		k.RevokedAt = &t
	}
	return k, nil
}

func isNameClash(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed: api_keys.profile_id, api_keys.name")
}

// Create issues a key for the profile. The returned secret is the only time
// the key exists in the clear: the store keeps its hash.
func (s *Store) Create(ctx context.Context, profileID, name string, withContext bool) (Key, string, error) {
	if !validName(name) {
		return Key{}, "", ErrInvalidName
	}
	secret, err := GenerateKey()
	if err != nil {
		return Key{}, "", err
	}
	id, err := newID()
	if err != nil {
		return Key{}, "", err
	}
	now := s.now().UTC()
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO api_keys (id, profile_id, name, prefix, key_hash, context, created_at) VALUES (?,?,?,?,?,?,?)`,
		id, profileID, name, secret[:prefixLen], HashKey(secret), boolInt(withContext), now.Format(timeFmt))
	if isNameClash(err) {
		return Key{}, "", ErrNameTaken
	}
	if err != nil {
		return Key{}, "", fmt.Errorf("storing api key: %w", err)
	}
	key, err := s.byID(ctx, profileID, id)
	return key, secret, err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (s *Store) byID(ctx context.Context, profileID, id string) (Key, error) {
	k, err := scanKey(s.db.QueryRowContext(ctx, selectKey+` WHERE profile_id = ? AND id = ?`, profileID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Key{}, ErrNotFound
	}
	return k, err
}

// Get finds a key of the profile by id, or by the name of an active key.
func (s *Store) Get(ctx context.Context, profileID, ref string) (Key, error) {
	k, err := s.byID(ctx, profileID, ref)
	if !errors.Is(err, ErrNotFound) {
		return k, err
	}
	k, err = scanKey(s.db.QueryRowContext(ctx,
		selectKey+` WHERE profile_id = ? AND name = ? AND revoked_at IS NULL`, profileID, ref))
	if errors.Is(err, sql.ErrNoRows) {
		return Key{}, ErrNotFound
	}
	return k, err
}

// List returns the profile's keys, oldest first. Revoked keys only with
// includeRevoked.
func (s *Store) List(ctx context.Context, profileID string, includeRevoked bool) ([]Key, error) {
	q := selectKey + ` WHERE profile_id = ?`
	if !includeRevoked {
		q += ` AND revoked_at IS NULL`
	}
	return s.list(ctx, q+` ORDER BY created_at, id`, profileID)
}

// ListAll returns every profile's keys, grouped by profile.
func (s *Store) ListAll(ctx context.Context, includeRevoked bool) ([]Key, error) {
	q := selectKey
	if !includeRevoked {
		q += ` WHERE revoked_at IS NULL`
	}
	return s.list(ctx, q+` ORDER BY profile_id, created_at, id`)
}

func (s *Store) list(ctx context.Context, q string, args ...any) ([]Key, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("listing api keys: %w", err)
	}
	defer rows.Close()
	out := []Key{}
	for rows.Next() {
		k, err := scanKey(rows)
		if err != nil {
			return nil, fmt.Errorf("reading api key: %w", err)
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// Update renames an active key and/or changes its context switch.
func (s *Store) Update(ctx context.Context, profileID, ref string, u Update) (Key, error) {
	k, err := s.Get(ctx, profileID, ref)
	if err != nil {
		return Key{}, err
	}
	if k.RevokedAt != nil {
		return Key{}, ErrNotFound
	}
	name, withContext := k.Name, k.Context
	if u.Name != nil {
		if !validName(*u.Name) {
			return Key{}, ErrInvalidName
		}
		name = *u.Name
	}
	if u.Context != nil {
		withContext = *u.Context
	}
	_, err = s.db.ExecContext(ctx, `UPDATE api_keys SET name = ?, context = ? WHERE id = ?`, name, boolInt(withContext), k.ID)
	if isNameClash(err) {
		return Key{}, ErrNameTaken
	}
	if err != nil {
		return Key{}, fmt.Errorf("updating api key: %w", err)
	}
	return s.byID(ctx, profileID, k.ID)
}

// Revoke ends a key now. Revoking an already revoked key succeeds and leaves
// it as it was.
func (s *Store) Revoke(ctx context.Context, profileID, ref string) (Key, error) {
	k, err := s.Get(ctx, profileID, ref)
	if err != nil {
		return Key{}, err
	}
	if k.RevokedAt != nil {
		return k, nil
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE api_keys SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`, s.now().UTC().Format(timeFmt), k.ID)
	if err != nil {
		return Key{}, fmt.Errorf("revoking api key: %w", err)
	}
	return s.byID(ctx, profileID, k.ID)
}

// RevokeProfile revokes every active key of the profile and reports how many.
func (s *Store) RevokeProfile(ctx context.Context, profileID string) (int, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE api_keys SET revoked_at = ? WHERE profile_id = ? AND revoked_at IS NULL`, s.now().UTC().Format(timeFmt), profileID)
	if err != nil {
		return 0, fmt.Errorf("revoking api keys of the profile: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// CountActive is the number of the profile's active keys.
func (s *Store) CountActive(ctx context.Context, profileID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM api_keys WHERE profile_id = ? AND revoked_at IS NULL`, profileID).Scan(&n)
	return n, err
}

// Authenticate resolves a presented key to its metadata. Every failure is
// ErrInvalidKey. It always reads the database, so a revocation takes effect
// on the next request.
func (s *Store) Authenticate(ctx context.Context, token string) (Key, error) {
	if !looksLikeKey(token) {
		return Key{}, ErrInvalidKey
	}
	want := HashKey(token)
	var stored string
	row := s.db.QueryRowContext(ctx, `SELECT key_hash, revoked_at IS NOT NULL FROM api_keys WHERE key_hash = ?`, want)
	var revoked bool
	if err := row.Scan(&stored, &revoked); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Key{}, ErrInvalidKey
		}
		return Key{}, fmt.Errorf("looking up api key: %w", err)
	}
	if revoked || subtle.ConstantTimeCompare([]byte(stored), []byte(want)) != 1 {
		return Key{}, ErrInvalidKey
	}
	k, err := scanKey(s.db.QueryRowContext(ctx, selectKey+` WHERE key_hash = ?`, want))
	if err != nil {
		return Key{}, fmt.Errorf("reading api key: %w", err)
	}
	s.touch(ctx, k.ID)
	return k, nil
}

// touch records the key's last use, at most once per lastUsedEvery.
func (s *Store) touch(ctx context.Context, id string) {
	now := s.now()
	s.mu.Lock()
	if last, ok := s.touched[id]; ok && now.Sub(last) < lastUsedEvery {
		s.mu.Unlock()
		return
	}
	s.touched[id] = now
	s.mu.Unlock()
	_, _ = s.db.ExecContext(ctx, `UPDATE api_keys SET last_used_at = ? WHERE id = ?`, now.UTC().Format(timeFmt), id)
}
