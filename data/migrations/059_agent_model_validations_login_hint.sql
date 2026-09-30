-- 059_agent_model_validations_login_hint.sql
--
-- The sign-in command monomind's `agent test --json` reports with an auth
-- failure (error.login_hint), so `agent validate`, `agent roster` and the
-- GUI can show it on the row (monoes/mono-agent#271).
ALTER TABLE agent_model_validations ADD COLUMN login_hint TEXT NOT NULL DEFAULT '';
