-- 041_org_unification.sql
-- Org × workflow unification (docs/plans/2026-09-15-org-workflow-unification.md
-- §6.2, contracts doc docs/plans/2026-09-16-org-unification-contracts.md §6).
--
-- The org JSON (<profile root>/.monomind/orgs/<name>.json) is the design copy
-- and is writable by any org role whose fileWrite reaches .monomind/ (C-3,
-- C-54). Everything that grants power therefore lives here instead:
--   org_grants        which automations a role may call, and how (layer 1)
--   org_endpoints     capability ids of automation roles' HTTP endpoints
--   org_bridge_calls  audit + hop/repeat loop control for every crossing (U10)
--   org_autonomy      the level/decider actually enforced (raise only via CLI/GUI/chat)
--   org_decisions     one row per decision routed, resolved, escalated, or failed
--   org_asks          org.ask node <-> reply correlation
-- Reconcile only ever strips JSON, revokes rows, or lowers a level; rows are
-- created only by explicit commands.
--
-- workflow_executions.resume_after lets event-woken pauses (org.run, org.ask,
-- waiting automations) skip the engine's 3-second resume poll until a bridge
-- event resumes them or the safety window passes (C-33).

CREATE TABLE IF NOT EXISTS org_grants (
  id             TEXT PRIMARY KEY,           -- "grt_" + 22 base32 chars
  profile_id     TEXT NOT NULL,
  org_name       TEXT NOT NULL,
  role_id        TEXT NOT NULL,
  tools_json     TEXT NOT NULL,              -- [{"tool","workflow_id","alias","mode","wait","timeout","approval","tier","max_calls_per_run","max_calls_per_day","max_output_bytes"}]
  org_tools_json TEXT NOT NULL DEFAULT '[]', -- holding/decider tools: [{"tool":"org_start","orgs":["sales"]}]
  created_at     TEXT NOT NULL,
  updated_at     TEXT NOT NULL,
  revoked_at     TEXT
);
CREATE INDEX IF NOT EXISTS org_grants_org ON org_grants(profile_id, org_name, role_id);

CREATE TABLE IF NOT EXISTS org_endpoints (
  id              TEXT PRIMARY KEY,          -- "ep_" + 26 base32 chars (128-bit capability in the URL path)
  profile_id      TEXT NOT NULL,             -- the receiver resolves the profile from this row, never from its own --profile
  org_name        TEXT NOT NULL,
  role_id         TEXT NOT NULL,
  workflow_id     TEXT NOT NULL,
  credential_file TEXT,
  rotated_from    TEXT,
  created_at      TEXT NOT NULL,
  revoked_at      TEXT
);
CREATE INDEX IF NOT EXISTS org_endpoints_org ON org_endpoints(profile_id, org_name, role_id);

CREATE TABLE IF NOT EXISTS org_bridge_calls (
  id           TEXT PRIMARY KEY,
  profile_id   TEXT NOT NULL,
  chain_id     TEXT NOT NULL,
  hop          INTEGER NOT NULL,
  origin_org   TEXT NOT NULL,
  direction    TEXT NOT NULL,                -- role_tool | endpoint_in | workflow_out | endpoint_reply | org_start
  org_name     TEXT,
  role_id      TEXT,
  workflow_id  TEXT,
  execution_id TEXT,
  grant_id     TEXT,
  endpoint_id  TEXT,
  run_id       TEXT,                         -- org run the call belongs to (per-run caps)
  status       TEXT NOT NULL,                -- ok | refused_hops | refused_repeat | refused_grant | refused_cap | error
  created_at   TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS org_bridge_chain ON org_bridge_calls(profile_id, chain_id, created_at);
CREATE INDEX IF NOT EXISTS org_bridge_grant ON org_bridge_calls(grant_id, created_at);

CREATE TABLE IF NOT EXISTS org_autonomy (
  profile_id         TEXT NOT NULL,
  org_name           TEXT NOT NULL,
  level              TEXT NOT NULL,          -- manual | mid | full
  decider_json       TEXT NOT NULL,
  tiers_json         TEXT NOT NULL DEFAULT '{}',
  policy_text        TEXT NOT NULL DEFAULT '',
  on_decider_failure TEXT NOT NULL DEFAULT 'deny',
  limits_json        TEXT NOT NULL DEFAULT '{}',
  paused_until       TEXT,
  updated_at         TEXT NOT NULL,
  updated_by         TEXT NOT NULL,          -- cli | gui | chat | reconcile
  PRIMARY KEY (profile_id, org_name)
);

CREATE TABLE IF NOT EXISTS org_decisions (
  id          TEXT PRIMARY KEY,
  profile_id  TEXT NOT NULL,
  org_name    TEXT NOT NULL,
  run_id      TEXT,
  item_kind   TEXT NOT NULL,                 -- approval | gate | question | hil
  item_ref    TEXT NOT NULL,
  item_hash   TEXT NOT NULL,
  requester   TEXT,
  class       TEXT NOT NULL,
  tier        TEXT NOT NULL,
  level       TEXT NOT NULL,
  resolver    TEXT NOT NULL,                 -- rule | model:<id> | boss:<role> | parent:<org>:<role> | human
  verdict     TEXT NOT NULL,                 -- approved | denied | answered | escalated | failed
  answer_text TEXT,
  rationale   TEXT,
  cost_usd    REAL,
  latency_ms  INTEGER,
  chain_id    TEXT,
  created_at  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS org_decisions_org ON org_decisions(profile_id, org_name, created_at);
CREATE INDEX IF NOT EXISTS org_decisions_repeat ON org_decisions(profile_id, org_name, run_id, item_hash);
CREATE INDEX IF NOT EXISTS org_decisions_item ON org_decisions(profile_id, org_name, item_kind, item_ref);

CREATE TABLE IF NOT EXISTS org_delegations (  -- items handed to a boss or parent decider role (U17)
  id           TEXT PRIMARY KEY,
  profile_id   TEXT NOT NULL,
  org_name     TEXT NOT NULL,              -- org the item belongs to
  item_kind    TEXT NOT NULL,
  item_ref     TEXT NOT NULL,
  item_json    TEXT NOT NULL,              -- the routed item (kind, class, tier, requester, action, request id, summary)
  level        TEXT NOT NULL,
  decider_org  TEXT NOT NULL,              -- org of the deciding role
  decider_role TEXT NOT NULL,
  status       TEXT NOT NULL,              -- pending | resolved | expired
  created_at   TEXT NOT NULL,
  deadline_at  TEXT NOT NULL,
  resolved_at  TEXT
);
CREATE INDEX IF NOT EXISTS org_delegations_decider ON org_delegations(profile_id, decider_org, decider_role, status);

CREATE TABLE IF NOT EXISTS org_asks (
  id               TEXT PRIMARY KEY,
  profile_id       TEXT NOT NULL,
  org_name         TEXT NOT NULL,
  role_id          TEXT NOT NULL,
  endpoint_role_id TEXT NOT NULL,
  execution_id     TEXT NOT NULL,
  node_id          TEXT NOT NULL,
  status           TEXT NOT NULL,            -- waiting | replied | timed_out
  reply_json       TEXT,
  created_at       TEXT NOT NULL,
  deadline_at      TEXT NOT NULL,
  replied_at       TEXT
);
CREATE INDEX IF NOT EXISTS org_asks_exec ON org_asks(execution_id, node_id);

ALTER TABLE workflow_executions ADD COLUMN resume_after TEXT;
