-- data/migrations/034_vault_documents.sql
-- A profile-scoped document vault, sibling to vault_images, for uploaded
-- profile files (résumés, cover-letter drafts, LinkedIn exports, etc.)
-- that get indexed into monomind's Second Brain.

CREATE TABLE IF NOT EXISTS vault_documents (
    id          TEXT PRIMARY KEY,   -- "doc-001", "doc-002", ...
    seq         INTEGER NOT NULL UNIQUE,
    path        TEXT NOT NULL,
    filename    TEXT NOT NULL,
    size_bytes  INTEGER NOT NULL DEFAULT 0,
    source      TEXT NOT NULL DEFAULT 'upload',
    profile_id  TEXT NOT NULL DEFAULT 'default',
    created_at  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_vault_documents_profile ON vault_documents(profile_id);
