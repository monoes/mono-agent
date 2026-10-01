package apikeys

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/storage"
)

func newTestStore(t *testing.T) (*Store, *storage.Database) {
	t.Helper()
	db, err := storage.NewDatabase(filepath.Join(t.TempDir(), "apikeys-test.db"))
	if err != nil {
		t.Fatalf("NewDatabase: %v", err)
	}
	if err := db.ApplyMigrations(); err != nil {
		t.Fatalf("ApplyMigrations: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return NewStore(db.DB), db
}

func TestMigrationCreatesApiKeys(t *testing.T) {
	_, db := newTestStore(t)
	var name string
	err := db.DB.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'api_keys'`).Scan(&name)
	if err != nil {
		t.Fatalf("api_keys table missing: %v", err)
	}
}

func TestGenerateKeyShape(t *testing.T) {
	a, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := GenerateKey()
	if a == b {
		t.Fatal("two generated keys are identical")
	}
	if !strings.HasPrefix(a, KeyPrefix) || len(a) != keyLen {
		t.Fatalf("key %q: want prefix %q and length %d", a, KeyPrefix, keyLen)
	}
	if HashKey(a) == HashKey(b) || len(HashKey(a)) != 64 {
		t.Fatalf("hash must be a 64-char hex SHA-256 and differ per key")
	}
}

func TestCreateReturnsKeyOnceAndStoresOnlyItsHash(t *testing.T) {
	s, db := newTestStore(t)
	ctx := context.Background()

	key, secret, err := s.Create(ctx, "default", "ci runner", true)
	if err != nil {
		t.Fatal(err)
	}
	if key.ID == "" || !strings.HasPrefix(key.ID, "key_") || key.ProfileID != "default" || key.Name != "ci runner" || !key.Context {
		t.Fatalf("unexpected key %+v", key)
	}
	if !strings.HasPrefix(secret, KeyPrefix) || !strings.HasPrefix(secret, key.Prefix) || len(key.Prefix) != prefixLen {
		t.Fatalf("secret %q and prefix %q do not line up", secret, key.Prefix)
	}

	// The plaintext key is in no column of any row.
	rows, err := db.DB.Query(`SELECT id, profile_id, name, prefix, key_hash FROM api_keys`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, profile, name, prefix, hash string
		if err := rows.Scan(&id, &profile, &name, &prefix, &hash); err != nil {
			t.Fatal(err)
		}
		for _, col := range []string{id, profile, name, prefix, hash} {
			if strings.Contains(col, secret) {
				t.Fatalf("the plaintext key was stored in the database")
			}
		}
		if hash != HashKey(secret) {
			t.Fatalf("stored hash %q != HashKey(secret)", hash)
		}
	}
}

// Get resolves an id before a name, so a key named like another key's id would
// shadow it: `api key revoke key_…` would hit the wrong key.
func TestNamesThatLookLikeKeyIDsAreRefused(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	for _, bad := range []string{"key_isqzhh2a5itg", "KEY_ISQZHH2A5ITG", "key_abcdefghijkl"} {
		if _, _, err := s.Create(ctx, "default", bad, false); !errors.Is(err, ErrInvalidName) {
			t.Errorf("Create(%q) err = %v, want ErrInvalidName", bad, err)
		}
	}
	k, _, err := s.Create(ctx, "default", "key_prod", false) // not the shape of an id
	if err != nil {
		t.Fatalf("a name that merely starts with key_ is fine: %v", err)
	}
	shadow := "key_isqzhh2a5itg"
	if _, err := s.Update(ctx, "default", k.ID, Update{Name: &shadow}); !errors.Is(err, ErrInvalidName) {
		t.Errorf("renaming to the shape of an id: err = %v, want ErrInvalidName", err)
	}
}

func TestCreateValidatesAndRejectsDuplicateActiveNames(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	for _, bad := range []string{"", " lead", "-dash", strings.Repeat("a", 65), "semi;colon", "new\nline"} {
		if _, _, err := s.Create(ctx, "default", bad, false); !errors.Is(err, ErrInvalidName) {
			t.Errorf("Create(%q) err = %v, want ErrInvalidName", bad, err)
		}
	}

	first, _, err := s.Create(ctx, "default", "app", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Create(ctx, "default", "app", false); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("duplicate active name: err = %v, want ErrNameTaken", err)
	}
	// Another profile may reuse the name.
	if _, _, err := s.Create(ctx, "work", "app", false); err != nil {
		t.Fatalf("same name in another profile: %v", err)
	}
	// After a revoke the name is free again.
	if _, err := s.Revoke(ctx, "default", first.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Create(ctx, "default", "app", false); err != nil {
		t.Fatalf("name after revoke: %v", err)
	}
}

func TestAuthenticate(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	key, secret, _ := s.Create(ctx, "work", "svc", true)

	got, err := s.Authenticate(ctx, secret)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if got.ID != key.ID || got.ProfileID != "work" || !got.Context {
		t.Fatalf("authenticated %+v, want key %s of profile work with context", got, key.ID)
	}

	other, _ := GenerateKey()
	for name, token := range map[string]string{
		"empty":        "",
		"unknown key":  other,
		"wrong prefix": "sk-" + secret[len(KeyPrefix):],
		"truncated":    secret[:len(secret)-1],
		"legacy token": strings.Repeat("a", 64),
	} {
		if _, err := s.Authenticate(ctx, token); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("%s: err = %v, want ErrInvalidKey", name, err)
		}
	}

	if _, err := s.Revoke(ctx, "work", key.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, secret); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("revoked key: err = %v, want ErrInvalidKey (same as unknown)", err)
	}
}

func TestAuthenticateRecordsLastUsedAtMostOncePerMinute(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	clock := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return clock }

	key, secret, _ := s.Create(ctx, "default", "svc", false)
	if _, err := s.Authenticate(ctx, secret); err != nil {
		t.Fatal(err)
	}
	first, _ := s.Get(ctx, "default", key.ID)
	if first.LastUsedAt == nil || !first.LastUsedAt.Equal(clock) {
		t.Fatalf("LastUsedAt = %v, want %v", first.LastUsedAt, clock)
	}

	clock = clock.Add(30 * time.Second)
	_, _ = s.Authenticate(ctx, secret)
	again, _ := s.Get(ctx, "default", key.ID)
	if !again.LastUsedAt.Equal(*first.LastUsedAt) {
		t.Fatalf("LastUsedAt moved within a minute: %v", again.LastUsedAt)
	}

	clock = clock.Add(2 * time.Minute)
	_, _ = s.Authenticate(ctx, secret)
	later, _ := s.Get(ctx, "default", key.ID)
	if !later.LastUsedAt.Equal(clock) {
		t.Fatalf("LastUsedAt = %v, want %v after a minute", later.LastUsedAt, clock)
	}
}

func TestGetListUpdateRevokeAreProfileScoped(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	a, _, _ := s.Create(ctx, "default", "alpha", false)
	s.Create(ctx, "default", "beta", true)
	b, _, _ := s.Create(ctx, "work", "gamma", false)

	// Lookup by id or by active name.
	if got, err := s.Get(ctx, "default", "alpha"); err != nil || got.ID != a.ID {
		t.Fatalf("Get by name: %+v, %v", got, err)
	}
	// Another profile's key, by id or by name, is plain "not found".
	for _, ref := range []string{b.ID, "gamma"} {
		if _, err := s.Get(ctx, "default", ref); !errors.Is(err, ErrNotFound) {
			t.Errorf("Get(default, %q) = %v, want ErrNotFound", ref, err)
		}
		if _, err := s.Revoke(ctx, "default", ref); !errors.Is(err, ErrNotFound) {
			t.Errorf("Revoke(default, %q) = %v, want ErrNotFound", ref, err)
		}
	}

	list, err := s.List(ctx, "default", false)
	if err != nil || len(list) != 2 {
		t.Fatalf("List(default) = %d keys, %v; want 2", len(list), err)
	}
	all, err := s.ListAll(ctx, false)
	if err != nil || len(all) != 3 {
		t.Fatalf("ListAll = %d keys, %v; want 3", len(all), err)
	}

	// Update renames and flips context.
	newName, off := "alpha-2", false
	up, err := s.Update(ctx, "default", "alpha", Update{Name: &newName, Context: &off})
	if err != nil || up.Name != "alpha-2" || up.Context {
		t.Fatalf("Update: %+v, %v", up, err)
	}
	on := true
	up, err = s.Update(ctx, "default", "alpha-2", Update{Context: &on})
	if err != nil || !up.Context {
		t.Fatalf("Update context: %+v, %v", up, err)
	}
	clash := "beta"
	if _, err := s.Update(ctx, "default", "alpha-2", Update{Name: &clash}); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("rename onto an active name: err = %v, want ErrNameTaken", err)
	}

	// Revoke is idempotent and keeps the row visible with IncludeRevoked.
	if _, err := s.Revoke(ctx, "default", a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Revoke(ctx, "default", a.ID); err != nil {
		t.Fatalf("second revoke: %v", err)
	}
	active, _ := s.List(ctx, "default", false)
	withRevoked, _ := s.List(ctx, "default", true)
	if len(active) != 1 || len(withRevoked) != 2 {
		t.Fatalf("active=%d withRevoked=%d, want 1 and 2", len(active), len(withRevoked))
	}
	// A revoked key can't be updated.
	if _, err := s.Update(ctx, "default", a.ID, Update{Context: &on}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Update of a revoked key: err = %v, want ErrNotFound", err)
	}
}

func TestRevokeProfileAndCountActive(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	s.Create(ctx, "default", "one", false)
	s.Create(ctx, "default", "two", false)
	_, secretWork, _ := s.Create(ctx, "work", "three", false)

	if n, _ := s.CountActive(ctx, "default"); n != 2 {
		t.Fatalf("CountActive(default) = %d, want 2", n)
	}
	n, err := s.RevokeProfile(ctx, "default")
	if err != nil || n != 2 {
		t.Fatalf("RevokeProfile = %d, %v; want 2", n, err)
	}
	if n, _ := s.CountActive(ctx, "default"); n != 0 {
		t.Fatalf("CountActive after revoke = %d, want 0", n)
	}
	if _, err := s.Authenticate(ctx, secretWork); err != nil {
		t.Fatalf("another profile's key must survive: %v", err)
	}
}
