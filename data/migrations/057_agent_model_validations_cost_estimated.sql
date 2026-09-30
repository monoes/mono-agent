-- 057_agent_model_validations_cost_estimated.sql
--
-- Marks a validation's cost as an estimate. `monomind agent test --json`
-- (monomind#390) prices a turn from its pricing table when the runtime
-- reports no cost (monoes/mono-agent#225).
ALTER TABLE agent_model_validations ADD COLUMN cost_estimated INTEGER NOT NULL DEFAULT 0;
