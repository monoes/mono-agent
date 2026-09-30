-- 060_ai_chat_org_workers.sql
--
-- Dynamic-org workers kept per conversation (monoes/mono-agent#230): after
-- each run of a worker, the turn stores its session id, folder, model and
-- role, and its last report, so a later turn can load it as an idle
-- veteran the lead can message (resuming its session, or re-briefing it
-- with the report). One row per worker id per conversation, overwritten by
-- its latest run; rows go with their conversation.
CREATE TABLE IF NOT EXISTS ai_chat_org_workers (
    profile_id      TEXT    NOT NULL,
    conversation_id TEXT    NOT NULL REFERENCES ai_chat_conversations(id) ON DELETE CASCADE,
    agent_id        TEXT    NOT NULL,
    parent_id       TEXT    NOT NULL DEFAULT '',
    turn_id         TEXT    NOT NULL,
    role            TEXT    NOT NULL DEFAULT '',
    agent_type      TEXT    NOT NULL DEFAULT '',
    category        TEXT    NOT NULL DEFAULT '',
    access          TEXT    NOT NULL DEFAULT '',
    skills          TEXT    NOT NULL DEFAULT '[]', -- JSON array of skill names
    runtime         TEXT    NOT NULL,
    model           TEXT    NOT NULL DEFAULT '',
    effort          TEXT    NOT NULL DEFAULT '',
    session_id      TEXT    NOT NULL DEFAULT '',
    cwd             TEXT    NOT NULL DEFAULT '',
    report          TEXT    NOT NULL DEFAULT '',
    outcome         TEXT    NOT NULL DEFAULT '',
    allow_spawn     INTEGER NOT NULL DEFAULT 0,
    updated_at      TEXT    NOT NULL,
    PRIMARY KEY (conversation_id, agent_id)
);

CREATE INDEX IF NOT EXISTS idx_ai_chat_org_workers_updated ON ai_chat_org_workers(conversation_id, updated_at);
