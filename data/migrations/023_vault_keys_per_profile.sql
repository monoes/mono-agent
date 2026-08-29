-- 023_vault_keys_per_profile.sql
-- vault_keys used to hold exactly one row (CHECK (id = 1)): every profile's
-- secrets were encrypted under the same single DEK, wrapped by the same
-- fixed "kek" OS-keychain entry. profile_id on vault_secrets rows was a
-- filter, not a cryptographic boundary. Move to one DEK per profile.
--
-- The old singleton row is preserved as vault_keys_legacy (untouched, same
-- shape) rather than copied forward — the app-level migration routine
-- (internal/secrets) needs it to decrypt every profile's existing secrets
-- under the OLD key before re-encrypting them under a fresh per-profile key,
-- and it stays as a read-only safety net until every profile is confirmed
-- migrated. It is not read by any normal (non-migration) code path.

ALTER TABLE vault_keys RENAME TO vault_keys_legacy;

CREATE TABLE vault_keys (
    profile_id    TEXT PRIMARY KEY,
    wrapped_dek   BLOB NOT NULL,
    wrapped_nonce BLOB NOT NULL,
    created_at    TEXT NOT NULL
);
