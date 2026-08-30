package connections

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/monoes/mono-agent/internal/secrets"
)

// MigrateProfileBlobs re-encrypts one profile's connections.data and
// platform_oauth_credentials.client_secret blobs — encrypted via
// secrets.EncryptBlob/DecryptBlob, the same vaultenc:v1: scheme
// vault_secrets uses, but stored in this package's own tables — off the
// legacy pre-per-profile key onto the profile's own key.
//
// This is a companion to secrets.MigrateProfileVaultKeys, which only ever
// covered vault_secrets rows: connections.data and OAuth client secrets are
// encrypted through the exact same DEK/KEK mechanism but live in different
// tables, so switching to per-profile keys without also migrating these
// left every existing connection's data undecryptable — DecryptBlob was now
// trying the profile's new key against ciphertext still under the old
// shared one, which surfaces as "no connection found" (scanConnection
// aborts the whole lookup on a decrypt error), which in turn makes callers
// think the connection doesn't exist and fall back to a fresh OAuth login
// flow. Idempotent: a value that's already vaultenc-decryptable under the
// profile's own key round-trips to the same bytes, so re-running this after
// it already succeeded is a safe no-op per row (still costs a decrypt
// attempt, but changes nothing).
func MigrateProfileBlobs(ctx context.Context, db *sql.DB, profileID string) (migrated int, errs []error) {
	if n, e := migrateConnectionsData(ctx, db, profileID); true {
		migrated += n
		errs = append(errs, e...)
	}
	if n, e := migrateOAuthClientSecrets(ctx, db, profileID); true {
		migrated += n
		errs = append(errs, e...)
	}
	return migrated, errs
}

func migrateConnectionsData(ctx context.Context, db *sql.DB, profileID string) (migrated int, errs []error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, data FROM connections WHERE COALESCE(profile_id,'default') = ?`, profileID)
	if err != nil {
		return 0, []error{fmt.Errorf("connections.MigrateProfileBlobs: listing connections: %w", err)}
	}
	type row struct{ id, data string }
	var toMigrate []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.data); err == nil {
			toMigrate = append(toMigrate, r)
		}
	}
	rows.Close()

	for _, r := range toMigrate {
		plain, err := secrets.DecryptBlobLegacy(ctx, db, r.data)
		if err != nil {
			// Already migrated (decrypts fine under the profile's own key
			// already) or genuinely corrupt — either way DecryptBlobLegacy
			// failing isn't itself an error worth surfacing per-row; only
			// report it if re-encryption would have been needed and wasn't
			// possible. Try the current (per-profile) path to distinguish.
			if _, verifyErr := secrets.DecryptBlob(ctx, db, profileID, r.data); verifyErr == nil {
				continue // already on the new key — nothing to do
			}
			errs = append(errs, fmt.Errorf("connections.MigrateProfileBlobs: decrypting connection %s: %w", r.id, err))
			continue
		}
		newEncoded, err := secrets.EncryptBlob(ctx, db, profileID, plain)
		if err != nil {
			errs = append(errs, fmt.Errorf("connections.MigrateProfileBlobs: re-encrypting connection %s: %w", r.id, err))
			continue
		}
		if roundTrip, err := secrets.DecryptBlob(ctx, db, profileID, newEncoded); err != nil || string(roundTrip) != string(plain) {
			errs = append(errs, fmt.Errorf("connections.MigrateProfileBlobs: round-trip verification failed for connection %s", r.id))
			continue
		}
		if _, err := db.ExecContext(ctx, `UPDATE connections SET data = ? WHERE id = ?`, newEncoded, r.id); err != nil {
			errs = append(errs, fmt.Errorf("connections.MigrateProfileBlobs: updating connection %s: %w", r.id, err))
			continue
		}
		migrated++
	}
	return migrated, errs
}

func migrateOAuthClientSecrets(ctx context.Context, db *sql.DB, profileID string) (migrated int, errs []error) {
	rows, err := db.QueryContext(ctx,
		`SELECT platform, client_secret FROM platform_oauth_credentials WHERE COALESCE(profile_id,'default') = ? AND client_secret != ''`, profileID)
	if err != nil {
		// Table may not exist yet on a fresh install — not an error.
		return 0, nil
	}
	type row struct{ platform, secret string }
	var toMigrate []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.platform, &r.secret); err == nil {
			toMigrate = append(toMigrate, r)
		}
	}
	rows.Close()

	for _, r := range toMigrate {
		plain, err := secrets.DecryptBlobLegacy(ctx, db, r.secret)
		if err != nil {
			if _, verifyErr := secrets.DecryptBlob(ctx, db, profileID, r.secret); verifyErr == nil {
				continue
			}
			errs = append(errs, fmt.Errorf("connections.MigrateProfileBlobs: decrypting oauth client secret for %s: %w", r.platform, err))
			continue
		}
		newEncoded, err := secrets.EncryptBlob(ctx, db, profileID, plain)
		if err != nil {
			errs = append(errs, fmt.Errorf("connections.MigrateProfileBlobs: re-encrypting oauth client secret for %s: %w", r.platform, err))
			continue
		}
		if roundTrip, err := secrets.DecryptBlob(ctx, db, profileID, newEncoded); err != nil || string(roundTrip) != string(plain) {
			errs = append(errs, fmt.Errorf("connections.MigrateProfileBlobs: round-trip verification failed for oauth client secret %s", r.platform))
			continue
		}
		if _, err := db.ExecContext(ctx,
			`UPDATE platform_oauth_credentials SET client_secret = ? WHERE platform = ? AND profile_id = ?`,
			newEncoded, r.platform, profileID,
		); err != nil {
			errs = append(errs, fmt.Errorf("connections.MigrateProfileBlobs: updating oauth client secret for %s: %w", r.platform, err))
			continue
		}
		migrated++
	}
	return migrated, errs
}
