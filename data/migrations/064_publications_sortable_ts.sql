-- A normalized, fixed-width UTC timestamp (millisecond precision) that sorts as
-- text, so List/Stats can use an index instead of julianday(published_at).
-- The backfill must equal Go's publication.sortTS(parseTS(published_at)):
--   * sub-millisecond digits are TRUNCATED (strftime('%f') would round, and
--     SQLite's date parser rounds to the millisecond itself), so the fraction is
--     cut from the text and only whole seconds go through strftime;
--   * a published_at that is not RFC 3339 (needs T and a Z/+hh:mm zone) maps to
--     '0001-01-01T00:00:00.000Z', the zero time Go's parseTS yields.
ALTER TABLE publications ADD COLUMN published_ts TEXT NOT NULL DEFAULT '';
UPDATE publications SET published_ts = CASE
  WHEN t.head GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]'
   AND (t.zone = 'Z' OR t.zone GLOB '[+-][0-9][0-9]:[0-9][0-9]')
   AND t.frac NOT GLOB '*[^0-9]*'
   AND (substr(t.tail, 1, 1) <> '.' OR t.frac <> '')
   AND strftime('%s', t.head || t.zone) IS NOT NULL
  THEN strftime('%Y-%m-%dT%H:%M:%S', t.head || t.zone) || '.' || substr(t.frac || '000', 1, 3) || 'Z'
  ELSE '0001-01-01T00:00:00.000Z'
END
FROM (
  SELECT id, head, tail,
    CASE WHEN substr(tail, 1, 1) = '.' THEN substr(tail, 2, p - 2) ELSE '' END AS frac,
    CASE WHEN substr(tail, 1, 1) = '.' THEN substr(tail, p) ELSE tail END AS zone
  FROM (
    SELECT id, head, tail,
      min(coalesce(nullif(instr(tail, 'Z'), 0), 1000000000),
          coalesce(nullif(instr(tail, '+'), 0), 1000000000),
          coalesce(nullif(instr(tail, '-'), 0), 1000000000)) AS p
    FROM (SELECT id, substr(published_at, 1, 19) AS head, substr(published_at, 20) AS tail FROM publications)
  )
) AS t
WHERE t.id = publications.id;
CREATE INDEX IF NOT EXISTS publications_recent_ts ON publications(profile_id, published_ts DESC, id DESC);
