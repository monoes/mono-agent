-- Jev decider answers on org decisions (docs/plans/2026-09-25-jev-integration.md, WS4).
--
-- A decision made by (or first put to) the `jev` decider keeps Jev's answer
-- next to it: the top verdict's confidence and the full distribution as a
-- JSON object {"approve": 0.93, "deny": 0.07}. Both stay NULL for every
-- other resolver. SQLite adds one column per statement.
ALTER TABLE org_decisions ADD COLUMN confidence REAL;
ALTER TABLE org_decisions ADD COLUMN probabilities TEXT;
