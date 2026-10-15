-- Digest email: per-user opt-in (migration 00054) and its content
-- (internal/digest).

-- name: GetDigestPrefs :one
-- No row = never opted in (frequency off).
SELECT user_id, frequency, last_period, last_sent_at, updated_at
FROM user_digest_prefs
WHERE user_id = $1;

-- name: SetDigestFrequency :one
-- Keeps last_period: turning the digest off and on again inside a period
-- doesn't send that period twice.
INSERT INTO user_digest_prefs (user_id, frequency, updated_at)
VALUES (@user_id, @frequency, now())
ON CONFLICT (user_id) DO UPDATE
    SET frequency  = EXCLUDED.frequency,
        updated_at = now()
RETURNING user_id, frequency, last_period, last_sent_at, updated_at;

-- name: ListDigestRecipients :many
-- Opted-in users with an email address, with what the send task needs to
-- apply their visibility (admin = every library; the rating ceiling and
-- label restriction stored on the user, exactly what their tokens carry).
SELECT u.id, u.username, u.email::TEXT AS email, u.is_admin,
       u.max_content_rating, u.blocked_labels, u.allowed_labels,
       d.frequency, d.last_period, d.last_sent_at
FROM user_digest_prefs d
JOIN users u ON u.id = d.user_id
WHERE d.frequency <> 'off'
  AND u.email IS NOT NULL AND btrim(u.email) <> ''
ORDER BY u.id;

-- name: GetDigestRecipient :one
-- One user's row in the ListDigestRecipients shape, whatever their frequency
-- (the admin's "Send me a test digest now").
SELECT u.id, u.username, COALESCE(u.email, '')::TEXT AS email, u.is_admin,
       u.max_content_rating, u.blocked_labels, u.allowed_labels,
       COALESCE(d.frequency, 'off')::TEXT AS frequency, d.last_period, d.last_sent_at
FROM users u
LEFT JOIN user_digest_prefs d ON d.user_id = u.id
WHERE u.id = $1;

-- name: ClaimDigestPeriod :execrows
-- Claims period for a user before sending: 0 rows means it was already sent
-- (or claimed by an overlapping run), so the caller skips. A failed send
-- hands it back with ReleaseDigestPeriod.
UPDATE user_digest_prefs
SET last_period = sqlc.arg(period)::text, last_sent_at = now()
WHERE user_id = @user_id
  AND frequency <> 'off'
  AND last_period IS DISTINCT FROM sqlc.arg(period)::text;

-- name: ReleaseDigestPeriod :exec
-- Undoes ClaimDigestPeriod after a failed or skipped send, restoring the
-- previous period so the next run tries again.
UPDATE user_digest_prefs
SET last_period = sqlc.narg('previous_period'), last_sent_at = sqlc.narg('previous_sent_at')
WHERE user_id = @user_id AND last_period = sqlc.arg(period)::text;

-- name: UnsubscribeDigest :execrows
-- The email's unsubscribe link. Rows = 0 when the user has no row (already
-- off); that's still a success for the caller.
UPDATE user_digest_prefs
SET frequency = 'off', updated_at = now()
WHERE user_id = $1 AND frequency <> 'off';

-- name: ListDigestNewItems :many
-- What was added to the user's libraries in [since, until), as digest
-- entries, the newest per_kind of each kind:
--   movie - one per movie
--   show  - one per show, counting its new episodes (item_count)
--   album - one per album; subtitle is the artist
--   book  - one per ebook, comic or audiobook
-- Gated like the home page's Recently Added strip: library_ids (NULL =
-- admin), the rating ceiling (an episode without its own rating takes the
-- show's) and the label restriction; a show must be visible itself too.
-- kind_total is how many entries the kind had before the cut.
WITH new_items AS (
  SELECT mi.id, mi.type, mi.library_id, mi.title, mi.year, mi.parent_id,
         mi.created_at, mi.poster_path, mi.thumb_path, mi.content_rating, mi.labels
  FROM media_items mi
  WHERE mi.deleted_at IS NULL
    AND mi.created_at >= sqlc.arg(since)::timestamptz
    AND mi.created_at <  sqlc.arg(until)::timestamptz
    AND mi.type IN ('movie', 'episode', 'album', 'book', 'audiobook')
    AND (sqlc.narg('library_ids')::uuid[] IS NULL OR mi.library_id = ANY(sqlc.narg('library_ids')::uuid[]))
), entries AS (
  SELECT (CASE n.type WHEN 'movie' THEN 'movie' WHEN 'album' THEN 'album' ELSE 'book' END)::TEXT AS kind,
         n.type AS item_type, n.id AS ref_id, n.library_id, n.title, n.year,
         COALESCE(n.poster_path, n.thumb_path) AS poster_path,
         (CASE WHEN n.type = 'album' THEN COALESCE(ar.title, '') ELSE '' END)::TEXT AS subtitle,
         1::BIGINT AS item_count,
         n.created_at AS added_at
  FROM new_items n
  LEFT JOIN media_items ar ON ar.id = n.parent_id AND n.type = 'album'
  WHERE n.type IN ('movie', 'album', 'book', 'audiobook')
    AND (sqlc.narg('max_rating_rank')::int IS NULL
         OR content_rating_rank(n.content_rating) <= sqlc.narg('max_rating_rank')::int)
    AND media_item_labels_permitted(n.labels, n.parent_id, sqlc.narg('blocked_labels')::text[], sqlc.narg('allowed_labels')::text[])
  UNION ALL
  SELECT 'show'::TEXT, 'show'::TEXT, s.id, s.library_id, s.title, s.year,
         COALESCE(s.poster_path, s.thumb_path),
         ''::TEXT,
         COUNT(*)::BIGINT,
         MAX(n.created_at)
  FROM new_items n
  JOIN media_items se ON se.id = n.parent_id
  JOIN media_items s  ON s.id = (CASE WHEN se.type = 'show' THEN se.id ELSE se.parent_id END)
                     AND s.type = 'show' AND s.deleted_at IS NULL
  WHERE n.type = 'episode'
    AND (sqlc.narg('max_rating_rank')::int IS NULL
         OR content_rating_rank(COALESCE(n.content_rating, s.content_rating)) <= sqlc.narg('max_rating_rank')::int)
    AND media_item_labels_permitted(n.labels, n.parent_id, sqlc.narg('blocked_labels')::text[], sqlc.narg('allowed_labels')::text[])
    AND (sqlc.narg('max_rating_rank')::int IS NULL
         OR content_rating_rank(s.content_rating) <= sqlc.narg('max_rating_rank')::int)
    AND media_item_labels_permitted(s.labels, s.parent_id, sqlc.narg('blocked_labels')::text[], sqlc.narg('allowed_labels')::text[])
  GROUP BY s.id, s.library_id, s.title, s.year, s.poster_path, s.thumb_path
), ranked AS (
  SELECT e.*,
         ROW_NUMBER() OVER (PARTITION BY e.kind ORDER BY e.added_at DESC, e.title) AS rn,
         COUNT(*) OVER (PARTITION BY e.kind) AS kind_total
  FROM entries e
)
SELECT kind, item_type, ref_id, library_id, title, year, poster_path, subtitle,
       item_count, added_at, kind_total::BIGINT AS kind_total
FROM ranked
WHERE rn <= sqlc.arg(per_kind)::int
ORDER BY kind, added_at DESC, title;
