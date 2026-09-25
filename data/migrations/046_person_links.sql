-- People links: cross-platform "same human?" links between people rows
-- (docs/plans/2026-09-25-jev-integration.md, WS8).
--
-- Rows are never merged: people upserts key on (platform_username,
-- platform, profile_id), so a link is the only record that two rows are one
-- person. person_a < person_b always (the repository orders the pair), so
-- the UNIQUE constraint covers the pair in either order.
--
-- relation: 'same' (a suggested or confirmed link) or 'not_same' (a pair
-- judged different — kept so it is never re-evaluated or re-paid for).
-- status:   'suggested' (Jev or a rule proposed it), 'confirmed' (a human
-- agreed), 'dismissed' (a human or a confident Jev answer said no).
CREATE TABLE IF NOT EXISTS person_links (
    id         TEXT PRIMARY KEY,
    profile_id TEXT NOT NULL DEFAULT 'default',
    person_a   TEXT NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    person_b   TEXT NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    relation   TEXT NOT NULL CHECK (relation IN ('same','not_same')),
    status     TEXT NOT NULL CHECK (status IN ('suggested','confirmed','dismissed')),
    confidence REAL,
    source     TEXT NOT NULL DEFAULT '',
    model      TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    CHECK (person_a < person_b),
    UNIQUE (profile_id, person_a, person_b)
);
CREATE INDEX IF NOT EXISTS idx_person_links_status ON person_links(profile_id, status);
CREATE INDEX IF NOT EXISTS idx_person_links_b ON person_links(person_b);
