-- Selector health for browser automation packages
-- (docs/mastermind/specs/2026-09-25-browser-automation-packages-design.md §8.7).
--
-- One row per (automation, selector key). Written only by the batched
-- health observer (internal/automation/health_observer.go); read by
-- `automation doctor` and the Health tab.
--
-- ok_count counts every successful lookup, healed_count the subset that
-- needed a non-first candidate (or the Jev fallback). recent is a ring of
-- the last 10 outcomes, oldest first: 'o' ok on the first candidate,
-- 'h' healed, 'f' failed. Status (ok / decaying / broken) is derived from
-- it in Go (health_status.go), never stored.
CREATE TABLE IF NOT EXISTS automation_selector_health (
    automation_id        TEXT NOT NULL,
    selector_key         TEXT NOT NULL,
    ok_count             INTEGER NOT NULL DEFAULT 0,
    fail_count           INTEGER NOT NULL DEFAULT 0,
    healed_count         INTEGER NOT NULL DEFAULT 0,
    last_ok_at           TEXT,
    last_fail_at         TEXT,
    last_candidate_index INTEGER NOT NULL DEFAULT -1,
    recent               TEXT NOT NULL DEFAULT '',
    updated_at           TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    PRIMARY KEY (automation_id, selector_key)
);
