-- data/migrations/037_vault_documents_indexed.sql
-- Tracks whether each uploaded profile document was successfully indexed
-- into monomind's Second Brain (knowledge_ingest), so the GUI can show a
-- durable per-document indicator instead of a one-time upload toast.
-- indexed=0 covers both "not attempted yet" and "attempted and failed" —
-- index_error (nullable) distinguishes the latter and carries the reason
-- (e.g. monomind not installed, or this profile not yet monomind-init'd).

ALTER TABLE vault_documents ADD COLUMN indexed INTEGER NOT NULL DEFAULT 0;
ALTER TABLE vault_documents ADD COLUMN index_error TEXT;
