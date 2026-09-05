-- data/migrations/031_applications.sql
-- Job and tender application tracking: a shared pipeline (status lifecycle,
-- tags, audit trail) over kind-specific typed detail tables. See
-- docs/mastermind/specs/2026-09-05-applications-foundation-design.md.

CREATE TABLE IF NOT EXISTS applications (
    id          TEXT PRIMARY KEY,
    profile_id  TEXT NOT NULL DEFAULT 'default',
    kind        TEXT NOT NULL CHECK (kind IN ('job', 'tender')),
    status      TEXT NOT NULL CHECK (status IN ('pending', 'applied', 'rejected', 'cancelled')),
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_applications_profile ON applications(profile_id);
CREATE INDEX IF NOT EXISTS idx_applications_kind    ON applications(kind);
CREATE INDEX IF NOT EXISTS idx_applications_status  ON applications(status);

CREATE TABLE IF NOT EXISTS application_tags (
    application_id TEXT NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    tag            TEXT NOT NULL,
    PRIMARY KEY (application_id, tag)
);

CREATE TABLE IF NOT EXISTS application_status_log (
    id             TEXT PRIMARY KEY,
    application_id TEXT NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    from_status    TEXT,
    to_status      TEXT NOT NULL,
    actor          TEXT NOT NULL CHECK (actor IN ('user', 'system')),
    note           TEXT NOT NULL DEFAULT '',
    created_at     TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_app_status_log_app ON application_status_log(application_id);

CREATE TABLE IF NOT EXISTS job_details (
    application_id    TEXT PRIMARY KEY REFERENCES applications(id) ON DELETE CASCADE,
    company           TEXT NOT NULL,
    url               TEXT NOT NULL,
    location          TEXT NOT NULL DEFAULT '',
    description       TEXT NOT NULL DEFAULT '',
    compensation_min  REAL,
    compensation_max  REAL,
    currency          TEXT NOT NULL DEFAULT '',
    job_type          TEXT NOT NULL DEFAULT '',
    is_remote         BOOLEAN,
    source            TEXT NOT NULL DEFAULT 'manual',
    posted_at         TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS tender_details (
    application_id           TEXT PRIMARY KEY REFERENCES applications(id) ON DELETE CASCADE,
    issuing_org              TEXT NOT NULL,
    url                      TEXT NOT NULL,
    description              TEXT NOT NULL DEFAULT '',
    submission_deadline      TEXT NOT NULL,
    estimated_value          REAL,
    currency                 TEXT NOT NULL DEFAULT '',
    required_certifications  TEXT NOT NULL DEFAULT '',
    bid_documents_required   TEXT NOT NULL DEFAULT '',
    source                   TEXT NOT NULL DEFAULT 'manual',
    published_at             TEXT NOT NULL DEFAULT ''
);
