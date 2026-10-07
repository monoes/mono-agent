-- A normalized, fixed-width UTC timestamp (millisecond precision) that sorts as
-- text, so List/Stats can use an index instead of julianday(published_at).
ALTER TABLE publications ADD COLUMN published_ts TEXT NOT NULL DEFAULT '';
UPDATE publications SET published_ts = COALESCE(strftime('%Y-%m-%dT%H:%M:%fZ', published_at), '');
CREATE INDEX IF NOT EXISTS publications_recent_ts ON publications(profile_id, published_ts DESC, id DESC);
