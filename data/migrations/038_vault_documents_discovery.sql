-- Supports documents discovered by a recursive scan of the profile folder
-- (source='discovered', see vault.RegisterDiscoveredDocument) and
-- staleness detection: indexed_mtime/indexed_size_bytes capture a
-- document's (mtime, size) at the moment it was LAST SUCCESSFULLY
-- indexed via knowledge_ingest, so a later read can derive "unchanged
-- since index" from "modified since index" via a cheap stat() instead of
-- re-hashing file content -- the same (modTime, size) signal
-- internal/orgdesign/watch.go already uses for its own change detection,
-- chosen over a persisted content hash because documents (PDFs, DOCX) can
-- be far larger than the small JSON org configs that pattern was designed
-- for, and because a false-positive mismatch here only ever produces a
-- user-visible "Stale" badge -- prompting a harmless, idempotent
-- re-index, not a correctness problem.
--
-- Both columns are NULL for every row that predates this migration and
-- for any row that has never been successfully indexed -- callers MUST
-- treat a NULL baseline as "cannot be stale" (Stale=false), never compare
-- it as zero, or every already-indexed document flips to "Stale" the
-- first time this runs post-upgrade. See vault.computeStale.
ALTER TABLE vault_documents ADD COLUMN indexed_mtime INTEGER;
ALTER TABLE vault_documents ADD COLUMN indexed_size_bytes INTEGER;

-- Non-unique: speeds up ReconcileDiscoveredDocuments' per-profile path
-- lookups. Deliberately NOT a UNIQUE(profile_id, path) constraint --
-- vault.MoveFiles (internal/vault/vault.go) rewrites `path` for existing
-- rows during App.MoveProfileFolder, and a uniqueness constraint would add
-- a new mid-move failure mode to that already-working path. The
-- idempotency this feature actually needs is already guaranteed
-- structurally: discovery reconciliation runs from a single watcher
-- goroutine, so its own check-then-insert has no real concurrent racer.
CREATE INDEX IF NOT EXISTS idx_vault_documents_profile_path ON vault_documents(profile_id, path);
