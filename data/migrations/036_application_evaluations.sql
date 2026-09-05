-- data/migrations/036_application_evaluations.sql
-- Append-only fit-scoring history for job applications, mirroring
-- application_status_log's philosophy: re-evaluating never overwrites a
-- prior verdict, it adds a new row.

CREATE TABLE IF NOT EXISTS application_evaluations (
    id                TEXT PRIMARY KEY,
    application_id    TEXT NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    runtime           TEXT NOT NULL,
    eligibility_pass  BOOLEAN NOT NULL,
    language_pass     BOOLEAN NOT NULL,
    technical_score   REAL NOT NULL DEFAULT 0,
    experience_score  REAL NOT NULL DEFAULT 0,
    behavioral_score  REAL NOT NULL DEFAULT 0,
    career_score      REAL NOT NULL DEFAULT 0,
    location_pass     BOOLEAN NOT NULL,
    overall_score     REAL NOT NULL DEFAULT 0,
    verdict           TEXT NOT NULL,
    rationale         TEXT NOT NULL DEFAULT '',
    created_at        TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_application_evaluations_app ON application_evaluations(application_id);
