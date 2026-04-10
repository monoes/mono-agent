-- Add pid column to workflow_executions so Wails can kill external CLI processes
ALTER TABLE workflow_executions ADD COLUMN pid INTEGER NOT NULL DEFAULT 0;
