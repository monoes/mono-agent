-- 058_agent_model_outcome_events.sql
--
-- Roster quality from outcomes (monoes/mono-agent#230). agent_model_outcomes
-- keeps running counts per runtime and model; this table keeps each
-- dynamic-org worker result and each lead rating of one, with the worker's
-- role category, so the roster can compute a success rate per model per
-- category that decays with age. Budget refusals and cancelled runs are
-- never written here.
CREATE TABLE IF NOT EXISTS agent_model_outcome_events (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    runtime   TEXT    NOT NULL,
    model     TEXT    NOT NULL,
    category  TEXT    NOT NULL,
    kind      TEXT    NOT NULL, -- outcome | rating
    success   INTEGER NOT NULL,
    at        TEXT    NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_agent_model_outcome_events_at ON agent_model_outcome_events(at);
