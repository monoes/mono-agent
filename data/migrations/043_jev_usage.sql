-- TypeSafe Jev bookkeeping (docs/plans/2026-09-25-jev-integration.md, WS0).
--
-- jev_usage: one row per Jev request, success or failure. Counts only —
-- never request or answer content (plan D8). Estimated cost is derived at
-- read time from input_tokens (output tokens are free).
CREATE TABLE IF NOT EXISTS jev_usage (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    profile_id   TEXT    NOT NULL DEFAULT '',
    surface      TEXT    NOT NULL DEFAULT '',
    model        TEXT    NOT NULL DEFAULT '',
    questions    INTEGER NOT NULL DEFAULT 0,
    input_tokens INTEGER NOT NULL DEFAULT 0,
    latency_ms   INTEGER NOT NULL DEFAULT 0,
    ok           INTEGER NOT NULL DEFAULT 1,
    created_at   TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX IF NOT EXISTS idx_jev_usage_profile_time ON jev_usage(profile_id, created_at);

-- jev_suggestions: the latest cached Jev suggestion per subject, so a GUI
-- that polls a list never pays for the same answer twice.
CREATE TABLE IF NOT EXISTS jev_suggestions (
    profile_id TEXT NOT NULL,
    surface    TEXT NOT NULL,
    subject_id TEXT NOT NULL,
    answer     TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    PRIMARY KEY (profile_id, surface, subject_id)
);
