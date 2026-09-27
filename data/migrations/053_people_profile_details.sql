-- Full profile details on people, and removal of people rows that were
-- never people.
--
-- 1. New columns for what a profile read returns (LinkedIn first):
--    headline   - the tagline under the name
--    location   - where the person says they are
--    about      - the profile's About/bio text. Kept apart from
--                 introduction, which is the drafted outreach message the
--                 people review flow edits and sends.
--    experience - JSON array of positions ({title, company, company_url,
--                 employment_type, date_range, start, end, duration,
--                 location, location_type, description, skills})
--    education  - JSON array of schools ({school, school_url, degree,
--                 field_of_study, date_range, start, end, grade,
--                 activities, description, skills})
ALTER TABLE people ADD COLUMN headline TEXT;
ALTER TABLE people ADD COLUMN location TEXT;
ALTER TABLE people ADD COLUMN about TEXT;
ALTER TABLE people ADD COLUMN experience TEXT;
ALTER TABLE people ADD COLUMN education TEXT;

-- 2. Saving extracted LinkedIn posts took the last segment of a post's
--    permalink (/feed/update/urn:li:activity:<id>/) as a username, so each
--    post became a "person" named urn:li:activity:<id> with a profile link
--    that doesn't open. Hacker News items did the same with "item"
--    (news.ycombinator.com/item?id=<id>). Remove those rows.
--
--    Migrations run with foreign keys off, so nothing cascades: history
--    rows that pointed at them (workflow runs, posts) are kept and
--    unlinked; rows that only exist for a person (tags, links, messages,
--    status updates) go with it.
CREATE TEMP TABLE bogus_people AS
    SELECT id FROM people
    WHERE platform_username LIKE 'urn:li:%'
       OR (UPPER(platform) = 'HACKERNEWS' AND platform_username = 'item' AND full_name IS NULL);

UPDATE workflow_node_targets SET person_id = NULL WHERE person_id IN (SELECT id FROM bogus_people);
UPDATE posts SET person_id = NULL WHERE person_id IN (SELECT id FROM bogus_people);
DELETE FROM people_tags WHERE person_id IN (SELECT id FROM bogus_people);
DELETE FROM person_links WHERE person_a IN (SELECT id FROM bogus_people) OR person_b IN (SELECT id FROM bogus_people);
DELETE FROM person_messages WHERE person_id IN (SELECT id FROM bogus_people);
DELETE FROM person_status_updates WHERE person_id IN (SELECT id FROM bogus_people);
DELETE FROM people WHERE id IN (SELECT id FROM bogus_people);
DROP TABLE bogus_people;
