-- 040_ai_chat_conversation_foreign_keys.sql
-- ai_chat_turns and ai_chat_events were created by Go code
-- (internal/ai/chat_events.go's initChatEventTables, called from
-- AIStore.initTables on every process start) rather than by a numbered
-- migration, per the interactive-agent-chat plan's explicit instruction not
-- to compete with that Go-managed table with a migration of the same
-- tables. Neither table has ever had a foreign key back to
-- ai_chat_conversations, so DeleteConversation's application-level
-- "check for an active turn, then delete" was the ONLY thing standing
-- between a deleted conversation and orphaned turn/event rows — and, as the
-- companion fix to internal/ai/chat_events.go's DeleteConversation shows
-- empirically (see chat_events_test.go's
-- TestChatEvents_DeleteConversationRaceAgainstConcurrentCreateTurn), making
-- that check atomic against a concurrent CreateTurn is NOT sufficient on its
-- own: a StartChatTurn admitted in-memory before DeleteConversation's check
-- ran can still have its CreateTurn/AppendEvent calls reach the database
-- only AFTER the delete has already committed and released its lock, at
-- which point nothing but a real constraint stops the insert from
-- succeeding against a conversation_id that no longer exists — an orphaned,
-- invisible, un-stoppable turn writing events for a conversation nothing can
-- query. See
-- docs/mastermind/plans/2026-09-12-interactive-agent-chat-followups.md,
-- "DeleteConversation isn't atomic against a concurrent StartChatTurn".
--
-- Fixed at two independent levels:
--   1. Application level (internal/ai/chat_events.go): DeleteConversation's
--      existence check, active-turn check, and delete now run inside one
--      BEGIN IMMEDIATE transaction, closing the window where a concurrent
--      CreateTurn could commit BETWEEN the check and the delete.
--   2. Schema level (this migration): ai_chat_turns.conversation_id and
--      ai_chat_events.conversation_id both get a real foreign key to
--      ai_chat_conversations(id) ON DELETE CASCADE; ai_chat_events.turn_id
--      also gets one to ai_chat_turns(id) ON DELETE CASCADE, since that is
--      its own natural parent and every AppendEvent call in this codebase
--      (wails-app/app_chat.go's appendAndEmit, the only production writer)
--      is already structurally downstream of a successful CreateTurn for
--      the same turn ID. This is what actually stops the "insert arrives
--      after the conversation is already gone" case fix (1) cannot reach:
--      the late INSERT now fails outright instead of silently orphaning a
--      row.
--
-- SQLite cannot ALTER TABLE ADD FOREIGN KEY on an existing table, so both
-- tables are rebuilt with the standard SQLite recipe (create the new shape,
-- copy every existing row across untouched, drop the old table, rename) —
-- the same recipe as 014_people_profile_scoped_unique.sql and
-- 018_crawler_sessions_profile_scoped_unique.sql. Rebuild ai_chat_turns
-- FULLY (create/copy/drop/rename) before starting ai_chat_events: SQLite
-- 3.25+'s ALTER TABLE ... RENAME rewrites foreign key references in other
-- tables that already point at the renamed table by name, so ai_chat_turns
-- must already be back under its real name before ai_chat_events_v2's own
-- FOREIGN KEY (turn_id) REFERENCES ai_chat_turns(id) clause is created.
--
-- The bootstrap CREATE TABLE IF NOT EXISTS statements below (matching
-- initChatEventTables()'s shape byte-for-byte as of this migration) exist
-- only so this migration is safe to run on a completely fresh database,
-- where ApplyMigrations (this file) runs BEFORE AIStore.initTables ever
-- gets a chance to create these tables at all — on such a database the
-- "copy existing rows" steps below simply copy zero rows. On every real
-- upgrade (an existing database whose Go code already created these tables
-- and has been writing rows to them) the bootstrap statements are no-ops
-- and the rebuild below runs against the real, populated tables.
--
-- Deliberately NOT included: any PRAGMA foreign_key_check gate. A real
-- user's database may already hold orphaned turns/events rows from having
-- hit this exact bug before upgrading to a binary that fixes it — copying
-- those rows across (FK enforcement is off for the duration of every
-- migration; see ApplyMigrations) must keep them, not abort the upgrade or
-- silently drop them. Enforcement is for new writes going forward, not a
-- retroactive validation of rows that already exist.

CREATE TABLE IF NOT EXISTS ai_chat_conversations (
    id TEXT PRIMARY KEY,
    profile_id TEXT NOT NULL,
    backend TEXT NOT NULL,
    workflow_context TEXT NOT NULL DEFAULT '',
    runtime_id TEXT NOT NULL DEFAULT '',
    provider_id TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL DEFAULT '',
    session_id TEXT NOT NULL DEFAULT '',
    history_key TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_ai_chat_conversations_profile ON ai_chat_conversations(profile_id, updated_at DESC);

CREATE TABLE IF NOT EXISTS ai_chat_turns (
    id TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL,
    profile_id TEXT NOT NULL,
    owner_instance_id TEXT NOT NULL DEFAULT '',
    prompt TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active',
    reason TEXT NOT NULL DEFAULT '',
    last_committed_seq INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS ai_chat_events (
    profile_id TEXT NOT NULL,
    conversation_id TEXT NOT NULL,
    turn_id TEXT NOT NULL,
    seq INTEGER NOT NULL,
    created_at TEXT NOT NULL,
    version INTEGER NOT NULL,
    type TEXT NOT NULL,
    payload TEXT NOT NULL,
    PRIMARY KEY (profile_id, conversation_id, turn_id, seq)
);

-- Rebuild ai_chat_turns with a foreign key back to its conversation.
CREATE TABLE ai_chat_turns_v2 (
    id TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL,
    profile_id TEXT NOT NULL,
    owner_instance_id TEXT NOT NULL DEFAULT '',
    prompt TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active',
    reason TEXT NOT NULL DEFAULT '',
    last_committed_seq INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    FOREIGN KEY (conversation_id) REFERENCES ai_chat_conversations(id) ON DELETE CASCADE
);

INSERT INTO ai_chat_turns_v2 (
    id, conversation_id, profile_id, owner_instance_id, prompt, status, reason, last_committed_seq, created_at, updated_at
)
SELECT
    id, conversation_id, profile_id, owner_instance_id, prompt, status, reason, last_committed_seq, created_at, updated_at
FROM ai_chat_turns;

DROP TABLE ai_chat_turns;

ALTER TABLE ai_chat_turns_v2 RENAME TO ai_chat_turns;

CREATE INDEX IF NOT EXISTS idx_ai_chat_turns_conversation ON ai_chat_turns(conversation_id, profile_id, created_at DESC);

-- Rebuild ai_chat_events with foreign keys back to both its conversation and
-- its turn (ai_chat_turns has already been rebuilt and renamed back above).
CREATE TABLE ai_chat_events_v2 (
    profile_id TEXT NOT NULL,
    conversation_id TEXT NOT NULL,
    turn_id TEXT NOT NULL,
    seq INTEGER NOT NULL,
    created_at TEXT NOT NULL,
    version INTEGER NOT NULL,
    type TEXT NOT NULL,
    payload TEXT NOT NULL,
    PRIMARY KEY (profile_id, conversation_id, turn_id, seq),
    FOREIGN KEY (conversation_id) REFERENCES ai_chat_conversations(id) ON DELETE CASCADE,
    FOREIGN KEY (turn_id) REFERENCES ai_chat_turns(id) ON DELETE CASCADE
);

INSERT INTO ai_chat_events_v2 (
    profile_id, conversation_id, turn_id, seq, created_at, version, type, payload
)
SELECT
    profile_id, conversation_id, turn_id, seq, created_at, version, type, payload
FROM ai_chat_events;

DROP TABLE ai_chat_events;

ALTER TABLE ai_chat_events_v2 RENAME TO ai_chat_events;
