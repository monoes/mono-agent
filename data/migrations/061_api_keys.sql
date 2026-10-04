-- API keys for the OpenAI-compatible HTTP API (docs/mastermind/specs/
-- 2026-10-01-openai-compatible-api-design.md, section 8.1).
--
-- A key belongs to exactly one profile. Only the SHA-256 (hex) of the key is
-- stored, never the key itself, so verifying a request needs no vault and no
-- keyring. prefix is the first characters of the key, for display. context is
-- 1 when requests made with this key get the profile's knowledge added to the
-- prompt. Names are unique per profile among active keys only, so a revoked
-- key's name can be reused.
CREATE TABLE IF NOT EXISTS api_keys (
    id           TEXT PRIMARY KEY,
    profile_id   TEXT NOT NULL,
    name         TEXT NOT NULL,
    prefix       TEXT NOT NULL,
    key_hash     TEXT NOT NULL UNIQUE,
    context      INTEGER NOT NULL DEFAULT 0,
    created_at   TEXT NOT NULL,
    last_used_at TEXT,
    revoked_at   TEXT
);
CREATE INDEX IF NOT EXISTS idx_api_keys_profile ON api_keys(profile_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_api_keys_profile_name_active
    ON api_keys(profile_id, name) WHERE revoked_at IS NULL;
