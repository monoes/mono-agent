package secrets

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/storage"

	"github.com/zalando/go-keyring"
)

// seedLegacyEncryptedBlob encrypts plaintext under a fresh legacy-scheme
// DEK/KEK (the fixed pre-per-profile "kek" keychain account + a
// vault_keys_legacy row) and returns the encoded blob — reproducing exactly
// what data written before the per-profile redesign looks like, so
// MigrateSessionsToVault's DecryptBlobLegacy path has something real to
// decrypt.
func seedLegacyEncryptedBlob(t *testing.T, db *storage.Database, plaintext []byte) string {
	t.Helper()
	ctx := context.Background()

	kek := make([]byte, 32)
	for i := range kek {
		kek[i] = byte(i + 1)
	}
	if err := keyring.Set(keyringService, legacyKeyringAccount, hex.EncodeToString(kek)); err != nil {
		t.Fatalf("seeding legacy KEK: %v", err)
	}

	dek := make([]byte, 32)
	for i := range dek {
		dek[i] = byte(255 - i)
	}
	wrappedDEK, wrappedNonce, err := Encrypt(kek, dek)
	if err != nil {
		t.Fatalf("wrapping legacy DEK: %v", err)
	}
	if _, err := db.DB.ExecContext(ctx,
		`INSERT INTO vault_keys_legacy (id, wrapped_dek, wrapped_nonce, created_at) VALUES (1, ?, ?, ?)`,
		wrappedDEK, wrappedNonce, time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		t.Fatalf("seeding vault_keys_legacy: %v", err)
	}

	ciphertext, nonce, err := Encrypt(dek, plaintext)
	if err != nil {
		t.Fatalf("encrypting legacy blob: %v", err)
	}
	combined := append(nonce, ciphertext...)
	return blobPrefix + base64.StdEncoding.EncodeToString(combined)
}

func TestMigrateSessionsToVault_NoOpWhenNoLegacyRows(t *testing.T) {
	db := newSecretsTestDB(t)
	ctx := context.Background()

	migrated, total, err := MigrateSessionsToVault(ctx, db.DB)
	if err != nil {
		t.Fatalf("MigrateSessionsToVault: %v", err)
	}
	if migrated != 0 || total != 0 {
		t.Fatalf("expected a no-op (0, 0) with no rows at all, got migrated=%d total=%d", migrated, total)
	}
}

func TestMigrateSessionsToVault_MigratesLegacyEncryptedCookies(t *testing.T) {
	db := newSecretsTestDB(t)
	ctx := context.Background()

	legacyCookies := []byte(`[{"name":"sid","value":"legacy"}]`)
	encCookies := seedLegacyEncryptedBlob(t, db, legacyCookies)
	_, err := db.DB.Exec(
		`INSERT INTO crawler_sessions (username, platform, cookies_json, expiry, profile_id) VALUES (?, ?, ?, ?, ?)`,
		"alice", "instagram", encCookies, "2099-01-01 00:00:00", "default",
	)
	if err != nil {
		t.Fatalf("seeding legacy session: %v", err)
	}

	migrated, total, err := MigrateSessionsToVault(ctx, db.DB)
	if err != nil {
		t.Fatalf("MigrateSessionsToVault: %v", err)
	}
	if migrated != 1 || total != 1 {
		t.Fatalf("expected migrated=1 total=1, got migrated=%d total=%d", migrated, total)
	}

	var vaultRef string
	if err := db.DB.QueryRow(`SELECT vault_ref FROM crawler_sessions WHERE username = 'alice' AND platform = 'instagram'`).Scan(&vaultRef); err != nil {
		t.Fatalf("reading migrated row: %v", err)
	}
	if vaultRef == "" {
		t.Fatal("expected vault_ref to be populated")
	}
	resolved, _, err := DecryptFields(ctx, db.DB, "default", vaultRef)
	if err != nil {
		t.Fatalf("DecryptFields: %v", err)
	}
	if resolved["cookies"] != string(legacyCookies) {
		t.Fatalf("unexpected migrated cookies: %q", resolved["cookies"])
	}

	// Idempotency: a second run is a no-op.
	migrated2, total2, err := MigrateSessionsToVault(ctx, db.DB)
	if err != nil {
		t.Fatalf("second MigrateSessionsToVault: %v", err)
	}
	if migrated2 != 0 || total2 != 0 {
		t.Fatalf("expected second run to no-op, got migrated=%d total=%d", migrated2, total2)
	}
}

func TestMigrateSessionsToVault_ContinuesPastPerRowFailure(t *testing.T) {
	db := newSecretsTestDB(t)
	ctx := context.Background()

	// A row whose cookies_json is not validly encrypted (simulates
	// corruption) must be logged and skipped, not abort a second, valid row.
	_, err := db.DB.Exec(
		`INSERT INTO crawler_sessions (username, platform, cookies_json, expiry, profile_id) VALUES (?, ?, ?, ?, ?)`,
		"broken-user", "instagram", "not-a-valid-vaultenc-blob", "2099-01-01 00:00:00", "default",
	)
	if err != nil {
		t.Fatalf("seeding broken session: %v", err)
	}
	goodCookies := []byte(`[{"name":"sid","value":"ok"}]`)
	encCookies := seedLegacyEncryptedBlob(t, db, goodCookies)
	_, err = db.DB.Exec(
		`INSERT INTO crawler_sessions (username, platform, cookies_json, expiry, profile_id) VALUES (?, ?, ?, ?, ?)`,
		"good-user", "instagram", encCookies, "2099-01-01 00:00:00", "default",
	)
	if err != nil {
		t.Fatalf("seeding good session: %v", err)
	}

	migrated, total, err := MigrateSessionsToVault(ctx, db.DB)
	if err != nil {
		t.Fatalf("MigrateSessionsToVault: %v", err)
	}
	if total != 2 {
		t.Fatalf("expected total=2, got %d", total)
	}
	if migrated != 1 {
		t.Fatalf("expected migrated=1 (only the good row), got %d", migrated)
	}
}
