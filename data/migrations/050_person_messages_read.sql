-- Read state for inbound messages: read_at is when a person read it (NULL =
-- unread). Messages that already exist are treated as read, so turning this
-- on doesn't flag a whole synced history as unread. Only new inbound
-- messages start unread.
ALTER TABLE person_messages ADD COLUMN read_at TIMESTAMP;
UPDATE person_messages SET read_at = created_at WHERE direction = 'inbound';

CREATE INDEX IF NOT EXISTS idx_person_messages_unread
    ON person_messages(profile_id, person_id)
    WHERE read_at IS NULL AND direction = 'inbound';
