-- Composite index for per-workflow execution listing: ListExecutions filters
-- by workflow_id and orders by created_at DESC, which was a full scan + sort
-- on large histories.
CREATE INDEX IF NOT EXISTS idx_workflow_executions_wf_created
    ON workflow_executions(workflow_id, created_at);
