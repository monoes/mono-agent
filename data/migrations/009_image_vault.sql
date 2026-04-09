-- 009_image_vault.sql
CREATE TABLE IF NOT EXISTS vault_images (
    id           TEXT PRIMARY KEY,       -- "img-001", "img-002", ...
    seq          INTEGER NOT NULL UNIQUE, -- numeric part, used for ordering and @-refs
    path         TEXT NOT NULL,           -- ~/.monoes/vault/img-001.png
    filename     TEXT NOT NULL,           -- original filename for display
    size_bytes   INTEGER NOT NULL DEFAULT 0,
    source       TEXT NOT NULL DEFAULT 'upload', -- 'gemini' | 'upload' | 'huggingface'
    workflow_id  TEXT,                    -- nullable FK to workflows
    execution_id TEXT,                    -- nullable FK to workflow_executions
    label        TEXT,                    -- optional user-set name
    created_at   TIMESTAMP NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_vault_images_seq ON vault_images(seq DESC);
