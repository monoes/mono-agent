-- Index for the dashboard's profile-wide run queries (`summary`,
-- `workflow executions --all`): they filter by profile and order/filter by a
-- normalised created_at, because stored timestamps come in three text shapes
-- (see internal/summary sinceExpr — the expression must stay identical).
-- Without it every poll sorts the profile's whole run history.
CREATE INDEX IF NOT EXISTS idx_workflow_executions_profile_time
    ON workflow_executions(profile_id, julianday(replace(substr(created_at,1,19),'T',' ')));

-- The live counts (running / queued / waiting) read only in-flight rows.
CREATE INDEX IF NOT EXISTS idx_workflow_executions_profile_live
    ON workflow_executions(profile_id, status)
    WHERE status IN ('RUNNING', 'QUEUED', 'WAITING');
