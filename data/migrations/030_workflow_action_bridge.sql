-- 030_workflow_action_bridge.sql
-- Introduces the two tables that let a workflow node run (specifically
-- BrowserNode, the node type that already wraps every social-platform
-- "action" as e.g. instagram.like_posts) persist what the standalone
-- Actions page used to persist directly into the actions/action_targets
-- tables:
--
--   workflow_node_targets  — one row per target a node interacted with
--                            (a person liked/commented-on/DM'd/followed/...),
--                            mirroring action_targets' shape but keyed by
--                            (execution_id, node_id) instead of action_id.
--   workflow_daily_counters — date-keyed per-profile, per-node-type counters
--                             for enforcing daily rate caps, mirroring
--                             action_daily_counters but keyed by node_type
--                             (e.g. "instagram.follow_users") instead of
--                             action_type.
--
-- This migration only adds schema. The actual data migration — converting
-- existing actions/action_targets rows into workflows/workflow_nodes/
-- workflow_executions/workflow_node_targets rows and then dropping
-- actions/action_targets/action_daily_counters — needs per-row JSON
-- construction and UUID generation that plain SQL can't do reliably across
-- SQLite builds, so it runs as a Go step (MigrateActionsToWorkflows in
-- internal/storage/actions_migration.go) immediately after this migration
-- applies. That Go step is itself idempotent: it no-ops once the `actions`
-- table is gone, which is also how it marks itself done.

CREATE TABLE IF NOT EXISTS workflow_node_targets (
    id                 TEXT PRIMARY KEY,
    execution_id       TEXT REFERENCES workflow_executions(id) ON DELETE CASCADE,
    node_id            TEXT NOT NULL,
    person_id          TEXT REFERENCES people(id),
    platform           TEXT NOT NULL,
    link               TEXT,
    status             TEXT NOT NULL DEFAULT 'PENDING',
    last_interacted_at TEXT,
    comment_text       TEXT,
    metadata           TEXT,
    created_at         TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_wnt_execution ON workflow_node_targets(execution_id);
CREATE INDEX IF NOT EXISTS idx_wnt_node ON workflow_node_targets(node_id);
CREATE INDEX IF NOT EXISTS idx_wnt_person ON workflow_node_targets(person_id);
CREATE INDEX IF NOT EXISTS idx_wnt_status ON workflow_node_targets(status);
CREATE INDEX IF NOT EXISTS idx_wnt_platform ON workflow_node_targets(platform);

CREATE TABLE IF NOT EXISTS workflow_daily_counters (
    profile_key TEXT NOT NULL,
    node_type   TEXT NOT NULL,
    day         TEXT NOT NULL,
    count       INTEGER NOT NULL DEFAULT 0,
    updated_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (profile_key, node_type, day)
);
