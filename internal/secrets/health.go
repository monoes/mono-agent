package secrets

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/zalando/go-keyring"
)

// VaultState is the outcome of CheckVault.
type VaultState string

const (
	// VaultOK: the profile's KEK is in the keychain and unwraps its DEK.
	VaultOK VaultState = "ok"
	// VaultUninitialized: no wrapped DEK yet — created on first secret save.
	VaultUninitialized VaultState = "uninitialized"
	// VaultKeyMissing: a wrapped DEK exists but the keychain has no KEK for
	// it — every stored secret of this profile is unreadable.
	VaultKeyMissing VaultState = "key-missing"
	// VaultKeyMismatch: the keychain KEK does not unwrap the stored DEK.
	VaultKeyMismatch VaultState = "key-mismatch"
	// VaultKeyringUnavailable: the OS keyring itself can't be reached.
	VaultKeyringUnavailable VaultState = "keyring-unavailable"
	// VaultFileKeyring: the OS keyring is unavailable and the opted-in file
	// keyring is in use; it is not unlocked here because that would prompt
	// for its passphrase.
	VaultFileKeyring VaultState = "file-keyring"
)

// CheckVault reports whether profileID's vault key chain is usable, without
// creating any key and without prompting — the read-only probe behind
// `monoagentcli doctor`. The returned error carries detail for the
// failing states.
func CheckVault(ctx context.Context, db *sql.DB, profileID string) (VaultState, error) {
	if profileID == "" {
		profileID = "default"
	}

	// The database first: a profile with no stored key never touches the
	// keychain, so a fresh or secret-less profile cannot raise an unlock
	// prompt (go-keyring's Linux Get unlocks the Secret Service collection).
	var hasTable int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'vault_keys'`).Scan(&hasTable); err != nil {
		return "", fmt.Errorf("inspecting vault_keys: %w", err)
	}
	if hasTable == 0 {
		return VaultUninitialized, nil
	}
	var wrappedDEK, wrappedNonce []byte
	err := db.QueryRowContext(ctx,
		`SELECT wrapped_dek, wrapped_nonce FROM vault_keys WHERE profile_id = ?`, profileID).
		Scan(&wrappedDEK, &wrappedNonce)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return VaultUninitialized, nil
	case err != nil:
		return "", fmt.Errorf("reading vault_keys: %w", err)
	}

	keyringIOMu.Lock()
	stored, kerr := keyringGet(keyringService, kekAccount(profileID))
	keyringIOMu.Unlock()
	if kerr != nil {
		// With the file keyring opted in, the real key lookup falls back to
		// it both when the OS keyring is unreachable and when it has no
		// entry, so neither means the secrets are unreadable. It is not
		// unlocked here: that would prompt for its passphrase.
		if fileKeyringEnabled() {
			return VaultFileKeyring, nil
		}
		if errors.Is(kerr, keyring.ErrNotFound) {
			return VaultKeyMissing, fmt.Errorf("no keychain entry %q/%q for this profile's stored key", keyringService, kekAccount(profileID))
		}
		return VaultKeyringUnavailable, kerr
	}

	kek, err := hex.DecodeString(stored)
	if err != nil {
		return VaultKeyMismatch, fmt.Errorf("decoding stored KEK: %w", err)
	}
	if _, err := Decrypt(kek, wrappedDEK, wrappedNonce); err != nil {
		return VaultKeyMismatch, fmt.Errorf("unwrapping DEK: %w", err)
	}
	return VaultOK, nil
}
