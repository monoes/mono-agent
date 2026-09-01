-- 029_action_daily_counters.sql
-- Date-keyed counters for enforcing daily rate caps (maxFollowsPerDay,
-- maxUnfollowsPerDay, maxRepliesPerDay, ...) across social actions.
--
-- Per-session caps (LoopDef.MaxItems) are already enforced in-memory for the
-- duration of one action run. Daily caps need a counter that survives across
-- runs and across days, hence a persisted table.
--
-- Keyed by (profile_key, action_type, day) rather than a numeric action row
-- ID: a daily cap is inherently about "how many follows has THIS PROFILE
-- done today", not "how many has THIS action run done" — the same
-- actionType (e.g. follow_users) may be re-triggered as a fresh action row
-- every day. profile_key holds profiles.id (see 011_profiles.sql), the same
-- scoping already used for the actions table itself; it is left
-- unconstrained (no FK) so a counter for a since-deleted profile doesn't
-- block deletion, mirroring how other profile_id columns in this schema are
-- handled.
--
-- day is stored as an ISO date string (YYYY-MM-DD) in UTC — the day boundary
-- is UTC to avoid DST edge cases (see docs/USAGE_POLICY.md).
CREATE TABLE IF NOT EXISTS action_daily_counters (
    profile_key TEXT NOT NULL,
    action_type TEXT NOT NULL,
    day         TEXT NOT NULL,
    count       INTEGER NOT NULL DEFAULT 0,
    updated_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (profile_key, action_type, day)
);
