-- data/migrations/035_vault_documents_application_link.sql
-- Links a generated document (CV, cover letter, tender proposal) back to
-- the job/tender application it was generated for. Nullable: a general
-- profile document (e.g. Phase 3's uploaded résumé) has none.

ALTER TABLE vault_documents ADD COLUMN application_id TEXT;
CREATE INDEX IF NOT EXISTS idx_vault_documents_application ON vault_documents(application_id);
