-- 027_vault_keys_per_profile.sql
-- (Renumbered from 023_vault_keys_per_profile.sql. schema_migrations keys on
-- the version INT alone, and two branches shipped *different* files at
-- 023/024 — databases from the other branch had those versions recorded and
-- would forever skip these. Slots 023/024 stay deliberately empty; nothing
-- re-uses them.)
--
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
--
-- The old->legacy RENAME originally lived here, but SQLite cannot guard an
-- ALTER TABLE in SQL, and databases that already carry the per-profile shape
-- (recorded as the other branch's 023/024) would fail the unguarded RENAME
-- outright. The RENAME therefore moved into storage.ReconcileSchema, which
-- PRAGMA-checks the actual table shape instead of trusting recorded versions
-- and runs right after ApplyMigrations on every database open. Only the
-- guardable statement remains here.

CREATE TABLE IF NOT EXISTS vault_keys (
    profile_id    TEXT PRIMARY KEY,
    wrapped_dek   BLOB NOT NULL,
    wrapped_nonce BLOB NOT NULL,
    created_at    TEXT NOT NULL
);
