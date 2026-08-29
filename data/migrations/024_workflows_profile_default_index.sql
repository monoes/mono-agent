-- 024_workflows_profile_default_index.sql
-- Expression index for the ListWorkflows profile filter. The query uses
-- COALESCE(profile_id,'default') = ?, which a plain index on profile_id
-- cannot serve (the expression hides the indexed column from the planner).

CREATE INDEX IF NOT EXISTS idx_workflows_profile_default
    ON workflows(COALESCE(profile_id, 'default'));
