package secrets

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"monoagent/internal/storage"

	"github.com/zalando/go-keyring"
)

// seedLegacySecret inserts a vault_secrets row encrypted under a fresh
// legacy-scheme DEK/KEK (the fixed pre-per-profile "kek" keychain account +
// a vault_keys_legacy row), reproducing exactly what a secret written before
// the per-profile redesign looks like on disk. Reuses the same legacy key
// across calls within one test (a real legacy install had exactly one).
func seedLegacySecret(t *testing.T, db *storage.Database, profileID, id, name string, fields map[string]string, notes string) {
	t.Helper()
	ctx := context.Background()

	var legacyDEK []byte
	var count int
	if err := db.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM vault_keys_legacy WHERE id = 1`).Scan(&count); err != nil {
		t.Fatalf("checking vault_keys_legacy: %v", err)
	}
	if count == 0 {
		kek := make([]byte, 32)
		for i := range kek {
			kek[i] = byte(i + 1)
		}
		if err := keyring.Set(keyringService, legacyKeyringAccount, hex.EncodeToString(kek)); err != nil {
			t.Fatalf("seeding legacy KEK: %v", err)
		}
		legacyDEK = make([]byte, 32)
		for i := range legacyDEK {
			legacyDEK[i] = byte(255 - i)
		}
		wrappedDEK, wrappedNonce, err := Encrypt(kek, legacyDEK)
		if err != nil {
			t.Fatalf("wrapping legacy DEK: %v", err)
		}
		if _, err := db.DB.ExecContext(ctx,
			`INSERT INTO vault_keys_legacy (id, wrapped_dek, wrapped_nonce, created_at) VALUES (1, ?, ?, ?)`,
			wrappedDEK, wrappedNonce, time.Now().UTC().Format(time.RFC3339),
		); err != nil {
			t.Fatalf("seeding vault_keys_legacy: %v", err)
		}
	} else {
		kek, found, err := fetchLegacyKEK()
		if err != nil || !found {
			t.Fatalf("re-reading legacy KEK: found=%v err=%v", found, err)
		}
		dek, found, err := fetchLegacyDEK(ctx, db.DB)
		if err != nil || !found {
			t.Fatalf("re-reading legacy DEK: found=%v err=%v", found, err)
		}
		_ = kek
		legacyDEK = dek
	}

	fieldsJSON, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshaling fields: %v", err)
	}
	ciphertext, nonce, err := Encrypt(legacyDEK, fieldsJSON)
	if err != nil {
		t.Fatalf("encrypting fields: %v", err)
	}
	var notesCiphertext, notesNonce []byte
	if notes != "" {
		notesCiphertext, notesNonce, err = Encrypt(legacyDEK, []byte(notes))
		if err != nil {
			t.Fatalf("encrypting notes: %v", err)
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.DB.ExecContext(ctx, `
		INSERT INTO vault_secrets (id, seq, profile_id, kind, name, ciphertext, nonce, notes_ciphertext, notes_nonce, created_at, updated_at, kv, field_count)
		VALUES (?, (SELECT COALESCE(MAX(seq),0)+1 FROM vault_secrets), ?, 'secret', ?, ?, ?, ?, ?, ?, ?, 1, ?)`,
		id, profileID, name, ciphertext, nonce, notesCiphertext, notesNonce, now, now, len(fields),
	); err != nil {
		t.Fatalf("inserting legacy vault_secrets row: %v", err)
	}
}

func TestMigrateProfileVaultKeys_ReencryptsUnderNewKeyAndIsolatesProfiles(t *testing.T) {
	db := newSecretsTestDB(t)
	ctx := context.Background()

	seedLegacySecret(t, db, "profile-a", "sec-901", "svc-a", map[string]string{"secret": "a-value"}, "a notes")
	seedLegacySecret(t, db, "profile-b", "sec-902", "svc-b", map[string]string{"secret": "b-value"}, "")

	migratedA, errsA := MigrateProfileVaultKeys(ctx, db.DB, "profile-a")
	if len(errsA) != 0 {
		t.Fatalf("migrating profile-a: %v", errsA)
	}
	if migratedA != 1 {
		t.Fatalf("expected 1 row migrated for profile-a, got %d", migratedA)
	}

	migratedB, errsB := MigrateProfileVaultKeys(ctx, db.DB, "profile-b")
	if len(errsB) != 0 {
		t.Fatalf("migrating profile-b: %v", errsB)
	}
	if migratedB != 1 {
		t.Fatalf("expected 1 row migrated for profile-b, got %d", migratedB)
	}

	// Each profile's secret still decrypts correctly under its own new key.
	fieldsA, notesA, err := DecryptFields(ctx, db.DB, "profile-a", "sec-901")
	if err != nil {
		t.Fatalf("DecryptFields profile-a after migration: %v", err)
	}
	if fieldsA["secret"] != "a-value" || notesA != "a notes" {
		t.Fatalf("profile-a data mismatch after migration: fields=%v notes=%q", fieldsA, notesA)
	}
	fieldsB, _, err := DecryptFields(ctx, db.DB, "profile-b", "sec-902")
	if err != nil {
		t.Fatalf("DecryptFields profile-b after migration: %v", err)
	}
	if fieldsB["secret"] != "b-value" {
		t.Fatalf("profile-b data mismatch after migration: fields=%v", fieldsB)
	}

	// Isolation: profile-b's key must not be able to decrypt profile-a's
	// (now re-encrypted) row, proving the two profiles are on genuinely
	// different keys, not just filtered by profile_id.
	dekB, err := getOrCreateDEK(ctx, db.DB, "profile-b")
	if err != nil {
		t.Fatalf("getOrCreateDEK profile-b: %v", err)
	}
	var ciphertextA, nonceA []byte
	if err := db.DB.QueryRow(`SELECT ciphertext, nonce FROM vault_secrets WHERE id = 'sec-901'`).Scan(&ciphertextA, &nonceA); err != nil {
		t.Fatalf("reading profile-a's re-encrypted row: %v", err)
	}
	if _, err := Decrypt(dekB, ciphertextA, nonceA); err == nil {
		t.Fatal("expected profile-b's key to fail decrypting profile-a's secret, but it succeeded")
	}

	// Idempotency: a second run for an already-migrated profile is a no-op.
	migratedAgain, errsAgain := MigrateProfileVaultKeys(ctx, db.DB, "profile-a")
	if len(errsAgain) != 0 {
		t.Fatalf("second migration run for profile-a: %v", errsAgain)
	}
	if migratedAgain != 0 {
		t.Fatalf("expected second run to be a no-op, got migrated=%d", migratedAgain)
	}
}

func TestMigrateProfileVaultKeys_NoOpWithNoLegacyKey(t *testing.T) {
	db := newSecretsTestDB(t)
	ctx := context.Background()

	migrated, errs := MigrateProfileVaultKeys(ctx, db.DB, "fresh-profile")
	if len(errs) != 0 {
		t.Fatalf("expected no errors on a fresh install with no legacy key, got %v", errs)
	}
	if migrated != 0 {
		t.Fatalf("expected 0 migrated with no legacy key, got %d", migrated)
	}
}
