-- 026_workflows_profile_default_index.sql
-- (Renumbered from 024_workflows_profile_default_index.sql when the merged
-- branch's vault migrations took 023/024; see 027's header for the full
-- story.)
-- Expression index for the ListWorkflows profile filter. The query uses
-- COALESCE(profile_id,'default') = ?, which a plain index on profile_id
-- cannot serve (the expression hides the indexed column from the planner).

CREATE INDEX IF NOT EXISTS idx_workflows_profile_default
    ON workflows(COALESCE(profile_id, 'default'));
