package secrets

import (
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"strings"
)

const blobPrefix = "vaultenc:v1:"

// EncryptBlob encrypts an arbitrary byte blob (e.g. a connections.data JSON
// document) under profileID's DEK and returns a self-describing string safe
// to store directly in a TEXT column.
func EncryptBlob(ctx context.Context, db *sql.DB, profileID string, plaintext []byte) (string, error) {
	dek, err := getOrCreateDEK(ctx, db, profileID)
	if err != nil {
		return "", fmt.Errorf("secrets.EncryptBlob: %w", err)
	}
	ciphertext, nonce, err := Encrypt(dek, plaintext)
	if err != nil {
		return "", fmt.Errorf("secrets.EncryptBlob: %w", err)
	}
	combined := append(nonce, ciphertext...)
	return blobPrefix + base64.StdEncoding.EncodeToString(combined), nil
}

// DecryptBlob reverses EncryptBlob. If encoded does not carry the vaultenc
// prefix (a row from before this feature shipped, not yet migrated) it is
// returned unchanged rather than erroring — callers that unmarshal JSON
// from the result get the original plaintext JSON either way.
func DecryptBlob(ctx context.Context, db *sql.DB, profileID, encoded string) ([]byte, error) {
	if !strings.HasPrefix(encoded, blobPrefix) {
		return []byte(encoded), nil
	}
	dek, err := getOrCreateDEK(ctx, db, profileID)
	if err != nil {
		return nil, fmt.Errorf("secrets.DecryptBlob: %w", err)
	}
	return decryptBlobWithDEK(dek, encoded)
}

// DecryptBlobLegacy reverses a blob encrypted before per-profile keys
// existed (the shared singleton DEK, preserved read-only in
// vault_keys_legacy by migration 023). Used only by pending pre-vault
// migrations (e.g. MigrateSessionsToVault) that may still encounter data
// written under the old scheme; every other path uses DecryptBlob with the
// owning profile's own key.
func DecryptBlobLegacy(ctx context.Context, db *sql.DB, encoded string) ([]byte, error) {
	if !strings.HasPrefix(encoded, blobPrefix) {
		return []byte(encoded), nil
	}
	dek, found, err := fetchLegacyDEK(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("secrets.DecryptBlobLegacy: %w", err)
	}
	if !found {
		return nil, fmt.Errorf("secrets.DecryptBlobLegacy: no legacy key found — nothing to migrate under it")
	}
	return decryptBlobWithDEK(dek, encoded)
}

func decryptBlobWithDEK(dek []byte, encoded string) ([]byte, error) {
	combined, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(encoded, blobPrefix))
	if err != nil {
		return nil, fmt.Errorf("secrets: decoding base64: %w", err)
	}
	const nonceSize = 12 // AES-GCM standard nonce size, matches crypto.go's gcm.NonceSize()
	if len(combined) < nonceSize {
		return nil, fmt.Errorf("secrets: encoded blob too short")
	}
	nonce, ciphertext := combined[:nonceSize], combined[nonceSize:]
	plaintext, err := Decrypt(dek, ciphertext, nonce)
	if err != nil {
		return nil, fmt.Errorf("secrets: %w", err)
	}
	return plaintext, nil
}
