-- data/migrations/032_application_titles.sql
-- Adds a title/position field to job and tender applications, distinct
-- from company/issuing_org — a gap discovered while designing Phase 2
-- (discovery), which needs to store the actual scraped job title.

ALTER TABLE job_details ADD COLUMN title TEXT NOT NULL DEFAULT '';
ALTER TABLE tender_details ADD COLUMN title TEXT NOT NULL DEFAULT '';
