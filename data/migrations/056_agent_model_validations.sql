-- 056_agent_model_validations.sql
--
-- The validated agent roster (dynamic org phase 0, monoes/mono-agent#225):
-- which runtime × model pairs actually answered a one-word test turn. Global
-- rather than per profile, because runtimes and their logins are
-- machine-wide.
--
-- agent_model_validations: the latest result per (runtime, model).
-- status is one of ok, ok_unexpected, auth, quota, model_unavailable,
-- timeout, missing_binary, error. source is "listed" (from the runtime's
-- model list) or "manual" (an id the user added). A manual row stays in the
-- roster even when the runtime no longer lists the model.
CREATE TABLE IF NOT EXISTS agent_model_validations (
    runtime          TEXT    NOT NULL,
    model            TEXT    NOT NULL,
    label            TEXT    NOT NULL DEFAULT '',
    effort_levels    TEXT    NOT NULL DEFAULT '[]',
    status           TEXT    NOT NULL,
    detail           TEXT    NOT NULL DEFAULT '',
    reply            TEXT    NOT NULL DEFAULT '',
    latency_first_ms INTEGER NOT NULL DEFAULT 0,
    latency_ms       INTEGER NOT NULL DEFAULT 0,
    tokens_in        INTEGER NOT NULL DEFAULT 0,
    tokens_out       INTEGER NOT NULL DEFAULT 0,
    cost_usd         REAL    NOT NULL DEFAULT 0,
    has_cost         INTEGER NOT NULL DEFAULT 0,
    runtime_version  TEXT    NOT NULL DEFAULT '',
    source           TEXT    NOT NULL DEFAULT 'listed',
    run_id           TEXT    NOT NULL DEFAULT '',
    validated_at     TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    PRIMARY KEY (runtime, model)
);

-- agent_model_validation_runs: one row per `agent validate` run.
CREATE TABLE IF NOT EXISTS agent_model_validation_runs (
    id          TEXT    PRIMARY KEY,
    started_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    finished_at TEXT,
    planned     INTEGER NOT NULL DEFAULT 0,
    ok          INTEGER NOT NULL DEFAULT 0,
    failed      INTEGER NOT NULL DEFAULT 0,
    cancelled   INTEGER NOT NULL DEFAULT 0
);

-- agent_model_outcomes: what real turns found out after validation. The
-- dynamic org conductor (phase 1) counts successes and failures here, and a
-- real auth/quota/model_unavailable failure also marks the roster row
-- failed, so the roster heals itself between validations.
CREATE TABLE IF NOT EXISTS agent_model_outcomes (
    runtime       TEXT    NOT NULL,
    model         TEXT    NOT NULL,
    successes     INTEGER NOT NULL DEFAULT 0,
    failures      INTEGER NOT NULL DEFAULT 0,
    last_status   TEXT    NOT NULL DEFAULT '',
    last_detail   TEXT    NOT NULL DEFAULT '',
    updated_at    TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    PRIMARY KEY (runtime, model)
);
