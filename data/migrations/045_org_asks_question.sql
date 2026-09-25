-- Org ask reply linking (docs/plans/2026-09-25-jev-integration.md, WS9).
--
-- org_asks.question: the question text an org.ask node sent, so a reply
-- that lost its "ask:<id>" token can still be linked to the waiting ask it
-- answers (opt-in `jev enable asks`). Nullable: rows written before this
-- migration keep NULL and are never offered as candidates.
ALTER TABLE org_asks ADD COLUMN question TEXT;
