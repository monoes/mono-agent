-- The personal task board (docs/mastermind/specs/2026-10-05-task-board-design.md,
-- section 4.2). A task always sits in one profile: deleting the profile deletes
-- its board. Times are fixed-width UTC text, so comparing text compares times.
-- position orders cards inside a column. client_id is an idempotency key from a
-- capture surface. claimed_by and claim_until are empty unless an agent holds
-- the task.
CREATE TABLE IF NOT EXISTS tasks (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    profile_id   TEXT NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    title        TEXT NOT NULL,
    notes        TEXT NOT NULL DEFAULT '',
    status       TEXT NOT NULL DEFAULT 'inbox'
                 CHECK (status IN ('inbox','ready','in_progress','review','done','archived')),
    position     INTEGER NOT NULL,
    source_kind  TEXT NOT NULL DEFAULT 'cli'
                 CHECK (source_kind IN ('cli','app','chrome','os','agent')),
    source_url   TEXT NOT NULL DEFAULT '',
    source_title TEXT NOT NULL DEFAULT '',
    source_app   TEXT NOT NULL DEFAULT '',
    client_id    TEXT,
    claimed_by   TEXT NOT NULL DEFAULT '',
    claim_until  TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_tasks_board ON tasks(profile_id, status, position);
CREATE UNIQUE INDEX IF NOT EXISTS idx_tasks_client ON tasks(profile_id, client_id) WHERE client_id IS NOT NULL;

-- One row per change of a task, written in the same transaction as the change.
CREATE TABLE IF NOT EXISTS task_events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id     INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    at          TEXT NOT NULL,
    actor       TEXT NOT NULL,
    kind        TEXT NOT NULL,
    from_status TEXT NOT NULL DEFAULT '',
    to_status   TEXT NOT NULL DEFAULT '',
    note        TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_task_events_task ON task_events(task_id, id);

-- One counter per profile, bumped by every write transaction, so a watcher
-- detects a change with a primary key read.
CREATE TABLE IF NOT EXISTS task_board_rev (
    profile_id TEXT PRIMARY KEY REFERENCES profiles(id) ON DELETE CASCADE,
    rev        INTEGER NOT NULL
);
