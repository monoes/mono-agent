-- Browser captures (the extension's page_capture envelopes in a profile's
-- .monomind/inbox/<ts>-<slug>/) are listed as documents: one row per
-- capture, pointing `path` at its primary artifact (readable.md, else the
-- PDF, else the MHTML archive). See vault.ReconcileCaptureDocuments.
--
-- url:         the page the capture was taken from (meta.json's canonical
--              URL, else the visited one). NULL for every other row.
-- capture_dir: the envelope directory. Non-NULL marks a capture-backed
--              row: only these are reconciled against the inbox, and
--              deleting one removes the whole envelope (deleting just the
--              primary file would make the next sync re-register the
--              capture under its next-best artifact).
ALTER TABLE vault_documents ADD COLUMN url TEXT;
ALTER TABLE vault_documents ADD COLUMN capture_dir TEXT;

CREATE INDEX IF NOT EXISTS idx_vault_documents_profile_capture ON vault_documents(profile_id, capture_dir);
