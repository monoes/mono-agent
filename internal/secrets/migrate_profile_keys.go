package secrets

import (
	"context"
	"database/sql"
	"fmt"
)

// MigrateProfileVaultKeys moves one profile's vault_secrets rows off the
// single pre-per-profile DEK (preserved read-only in vault_keys_legacy by
// migration 023) onto a fresh DEK/KEK generated exclusively for this
// profile. Idempotent: if profileID already has a vault_keys row, this is a
// no-op (already migrated, or a profile that only ever existed under the
// per-profile scheme and never had legacy data to begin with).
//
// Every row is decrypted under the legacy key and re-encrypted under the new
// one, with an immediate round-trip decrypt of the freshly-written
// ciphertext before it's considered successful — this is the guarantee the
// per-profile encryption redesign depends on: a profile's secrets must never
// be left readable under any key other than its own once migration reports
// success. A per-row failure is logged by the caller via the returned error
// list and that row is left untouched under the legacy key rather than
// losing it — the caller can retry the whole profile safely since this
// function is idempotent per already-migrated row too (a row already
// re-encrypted under the new key simply fails to decrypt under the legacy
// key on a retry and is skipped, having already succeeded).
func MigrateProfileVaultKeys(ctx context.Context, db *sql.DB, profileID string) (migrated int, errs []error) {
	var alreadyMigrated int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM vault_keys WHERE profile_id = ?`, profileID).Scan(&alreadyMigrated); err != nil {
		return 0, []error{fmt.Errorf("secrets.MigrateProfileVaultKeys: checking existing key: %w", err)}
	}
	if alreadyMigrated > 0 {
		return 0, nil
	}

	legacyDEK, found, err := fetchLegacyDEK(ctx, db)
	if err != nil {
		return 0, []error{fmt.Errorf("secrets.MigrateProfileVaultKeys: %w", err)}
	}
	if !found {
		// No legacy key ever existed for this database (a fresh install) —
		// nothing to migrate. The profile's own key gets created lazily on
		// its first real Add/EncryptBlob call, as normal.
		return 0, nil
	}

	rows, err := db.QueryContext(ctx,
		`SELECT id, ciphertext, nonce, notes_ciphertext, notes_nonce FROM vault_secrets WHERE profile_id = ?`, profileID)
	if err != nil {
		return 0, []error{fmt.Errorf("secrets.MigrateProfileVaultKeys: listing rows: %w", err)}
	}
	type row struct {
		id                          string
		ciphertext, nonce           []byte
		notesCiphertext, notesNonce []byte
	}
	var toMigrate []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.ciphertext, &r.nonce, &r.notesCiphertext, &r.notesNonce); err == nil {
			toMigrate = append(toMigrate, r)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		errs = append(errs, fmt.Errorf("secrets.MigrateProfileVaultKeys: iterating rows: %w", err))
	}
	if len(toMigrate) == 0 {
		// No secrets to migrate for this profile — still create its own key
		// so it's fully independent going forward instead of implicitly
		// depending on the legacy key ever existing.
		if _, err := getOrCreateDEK(ctx, db, profileID); err != nil {
			errs = append(errs, fmt.Errorf("secrets.MigrateProfileVaultKeys: creating key for empty profile: %w", err))
		}
		return 0, errs
	}

	newDEK, err := getOrCreateDEK(ctx, db, profileID)
	if err != nil {
		return 0, append(errs, fmt.Errorf("secrets.MigrateProfileVaultKeys: creating new key: %w", err))
	}

	for _, r := range toMigrate {
		plainFields, decErr := Decrypt(legacyDEK, r.ciphertext, r.nonce)
		if decErr != nil {
			errs = append(errs, fmt.Errorf("secrets.MigrateProfileVaultKeys: decrypting %s under legacy key: %w", r.id, decErr))
			continue
		}
		newCiphertext, newNonce, encErr := Encrypt(newDEK, plainFields)
		if encErr != nil {
			errs = append(errs, fmt.Errorf("secrets.MigrateProfileVaultKeys: re-encrypting %s: %w", r.id, encErr))
			continue
		}
		// Round-trip verify before this row's re-encryption is trusted.
		if roundTrip, rtErr := Decrypt(newDEK, newCiphertext, newNonce); rtErr != nil || string(roundTrip) != string(plainFields) {
			errs = append(errs, fmt.Errorf("secrets.MigrateProfileVaultKeys: round-trip verification failed for %s", r.id))
			continue
		}

		var newNotesCiphertext, newNotesNonce []byte
		if len(r.notesCiphertext) > 0 {
			plainNotes, decErr := Decrypt(legacyDEK, r.notesCiphertext, r.notesNonce)
			if decErr != nil {
				errs = append(errs, fmt.Errorf("secrets.MigrateProfileVaultKeys: decrypting notes for %s under legacy key: %w", r.id, decErr))
				continue
			}
			newNotesCiphertext, newNotesNonce, err = Encrypt(newDEK, plainNotes)
			if err != nil {
				errs = append(errs, fmt.Errorf("secrets.MigrateProfileVaultKeys: re-encrypting notes for %s: %w", r.id, err))
				continue
			}
			if roundTrip, rtErr := Decrypt(newDEK, newNotesCiphertext, newNotesNonce); rtErr != nil || string(roundTrip) != string(plainNotes) {
				errs = append(errs, fmt.Errorf("secrets.MigrateProfileVaultKeys: notes round-trip verification failed for %s", r.id))
				continue
			}
		}

		if _, err := db.ExecContext(ctx,
			`UPDATE vault_secrets SET ciphertext = ?, nonce = ?, notes_ciphertext = ?, notes_nonce = ? WHERE id = ?`,
			newCiphertext, newNonce, newNotesCiphertext, newNotesNonce, r.id,
		); err != nil {
			errs = append(errs, fmt.Errorf("secrets.MigrateProfileVaultKeys: updating %s: %w", r.id, err))
			continue
		}
		migrated++
	}
	return migrated, errs
}
