-- A user's own listening and viewing stats: My stats, Year in review and the
-- digest email's month in numbers (internal/userstats).
--
-- Every query reads the watch_plays analytics matview for ONE user over
-- [since, until), through the (user_id, occurred_at) index added in migration
-- 00054, and applies the same three gates as the user's history and every item
-- listing: library access (library_ids; NULL = admin, every library), the
-- content-rating ceiling (max_rating_rank; an episode or track without its own
-- rating takes its season/show or album/artist's) and the label restriction.
-- A title the user can no longer see drops out of their stats too, the same way
-- it drops out of their history.
--
-- "Time" is how far into the item each play got: its final position, capped
-- at the item's length. The admin dashboard sums the item's length instead,
-- which credits a whole film for a five-minute sample. A play is "finished" at
-- 90% of the length, the watched threshold used across the product.
--
-- Video: movie, episode, music_video, home_video. Audio: track, audiobook,
-- audiobook_chapter, podcast_episode. tz is a validated IANA zone name; days,
-- months and hours bucket in it.

-- name: GetUserStatsTotals :one
SELECT COUNT(*)::BIGINT                                                     AS plays,
       COALESCE(SUM(b.watched_ms) FILTER (WHERE b.kind = 'video'), 0)::BIGINT AS video_ms,
       COALESCE(SUM(b.watched_ms) FILTER (WHERE b.kind = 'audio'), 0)::BIGINT AS audio_ms,
       (COUNT(DISTINCT b.media_id) FILTER (WHERE b.finished))::BIGINT        AS items_finished,
       (COUNT(DISTINCT b.media_id) FILTER (WHERE b.finished
                                             AND b.type = 'audiobook'))::BIGINT AS audiobooks_finished,
       (COUNT(DISTINCT DATE(b.occurred_at AT TIME ZONE sqlc.arg(tz)::TEXT)))::BIGINT AS active_days
FROM (
  SELECT wp.media_id, wp.occurred_at, mi.type,
         CASE WHEN mi.type IN ('movie', 'episode', 'music_video', 'home_video') THEN 'video'
              WHEN mi.type IN ('track', 'audiobook', 'audiobook_chapter', 'podcast_episode') THEN 'audio'
              ELSE 'other' END AS kind,
         LEAST(GREATEST(wp.position_ms, 0),
               COALESCE(NULLIF(COALESCE(wp.duration_ms, mi.duration_ms), 0), GREATEST(wp.position_ms, 0))) AS watched_ms,
         (COALESCE(wp.duration_ms, mi.duration_ms, 0) > 0
          AND wp.position_ms >= COALESCE(wp.duration_ms, mi.duration_ms) * 9 / 10) AS finished
  FROM watch_plays wp
  JOIN media_items mi ON mi.id = wp.media_id AND mi.deleted_at IS NULL
  LEFT JOIN media_items p  ON p.id  = mi.parent_id
  LEFT JOIN media_items gp ON gp.id = p.parent_id
  WHERE wp.user_id = sqlc.arg(user_id)::uuid
    AND wp.occurred_at >= sqlc.arg(since)::timestamptz
    AND wp.occurred_at <  sqlc.arg(until)::timestamptz
    AND (sqlc.narg('library_ids')::uuid[] IS NULL OR mi.library_id = ANY(sqlc.narg('library_ids')::uuid[]))
    AND (sqlc.narg('max_rating_rank')::int IS NULL
         OR content_rating_rank(COALESCE(mi.content_rating, p.content_rating, gp.content_rating)) <= sqlc.narg('max_rating_rank')::int)
    AND media_item_labels_permitted(mi.labels, mi.parent_id, sqlc.narg('blocked_labels')::text[], sqlc.narg('allowed_labels')::text[])
) b;

-- name: GetUserStatsByType :many
-- Plays and time per media type (movie, episode, track, ...).
SELECT mi.type::TEXT AS type,
       COUNT(*)::BIGINT AS plays,
       COALESCE(SUM(LEAST(GREATEST(wp.position_ms, 0),
               COALESCE(NULLIF(COALESCE(wp.duration_ms, mi.duration_ms), 0), GREATEST(wp.position_ms, 0)))), 0)::BIGINT AS watched_ms
FROM watch_plays wp
JOIN media_items mi ON mi.id = wp.media_id AND mi.deleted_at IS NULL
LEFT JOIN media_items p  ON p.id  = mi.parent_id
LEFT JOIN media_items gp ON gp.id = p.parent_id
WHERE wp.user_id = sqlc.arg(user_id)::uuid
  AND wp.occurred_at >= sqlc.arg(since)::timestamptz
  AND wp.occurred_at <  sqlc.arg(until)::timestamptz
  AND (sqlc.narg('library_ids')::uuid[] IS NULL OR mi.library_id = ANY(sqlc.narg('library_ids')::uuid[]))
  AND (sqlc.narg('max_rating_rank')::int IS NULL
       OR content_rating_rank(COALESCE(mi.content_rating, p.content_rating, gp.content_rating)) <= sqlc.narg('max_rating_rank')::int)
  AND media_item_labels_permitted(mi.labels, mi.parent_id, sqlc.narg('blocked_labels')::text[], sqlc.narg('allowed_labels')::text[])
GROUP BY mi.type
ORDER BY watched_ms DESC, plays DESC;

-- name: GetUserStatsTopGenres :many
-- An episode or track rarely carries genres of its own; it takes the nearest
-- ancestor's (season/show, album/artist) that has any.
SELECT g.genre::TEXT AS genre,
       COUNT(*)::BIGINT AS plays,
       COALESCE(SUM(b.watched_ms), 0)::BIGINT AS watched_ms
FROM (
  SELECT CASE WHEN COALESCE(cardinality(mi.genres), 0) > 0 THEN mi.genres
              WHEN COALESCE(cardinality(p.genres), 0) > 0  THEN p.genres
              ELSE gp.genres END AS genres,
         LEAST(GREATEST(wp.position_ms, 0),
               COALESCE(NULLIF(COALESCE(wp.duration_ms, mi.duration_ms), 0), GREATEST(wp.position_ms, 0))) AS watched_ms
  FROM watch_plays wp
  JOIN media_items mi ON mi.id = wp.media_id AND mi.deleted_at IS NULL
  LEFT JOIN media_items p  ON p.id  = mi.parent_id
  LEFT JOIN media_items gp ON gp.id = p.parent_id
  WHERE wp.user_id = sqlc.arg(user_id)::uuid
    AND wp.occurred_at >= sqlc.arg(since)::timestamptz
    AND wp.occurred_at <  sqlc.arg(until)::timestamptz
    AND (sqlc.narg('library_ids')::uuid[] IS NULL OR mi.library_id = ANY(sqlc.narg('library_ids')::uuid[]))
    AND (sqlc.narg('max_rating_rank')::int IS NULL
         OR content_rating_rank(COALESCE(mi.content_rating, p.content_rating, gp.content_rating)) <= sqlc.narg('max_rating_rank')::int)
    AND media_item_labels_permitted(mi.labels, mi.parent_id, sqlc.narg('blocked_labels')::text[], sqlc.narg('allowed_labels')::text[])
) b
CROSS JOIN LATERAL unnest(b.genres) AS g(genre)
WHERE g.genre <> ''
GROUP BY g.genre
ORDER BY watched_ms DESC, plays DESC, g.genre
LIMIT sqlc.arg(lim)::int;

-- name: GetUserStatsTopTitles :many
-- The top lim titles in each category, by plays then time:
--   movie  - the movie
--   show   - the show an episode belongs to (episodes count as plays)
--   album  - the album a track belongs to
--   artist - the artist of that album (or of the track, when it hangs off
--            the artist directly)
-- The ranked title itself passes the rating ceiling and label restriction
-- too: a show rated above the ceiling stays out even when an episode of it
-- carries a lower rating of its own.
WITH b AS (
  SELECT mi.id, mi.type,
         CASE WHEN p.type = 'show' THEN p.id WHEN gp.type = 'show' THEN gp.id END AS show_id,
         CASE WHEN p.type = 'album' THEN p.id END AS album_id,
         CASE WHEN gp.type = 'artist' THEN gp.id WHEN p.type = 'artist' THEN p.id END AS artist_id,
         LEAST(GREATEST(wp.position_ms, 0),
               COALESCE(NULLIF(COALESCE(wp.duration_ms, mi.duration_ms), 0), GREATEST(wp.position_ms, 0))) AS watched_ms
  FROM watch_plays wp
  JOIN media_items mi ON mi.id = wp.media_id AND mi.deleted_at IS NULL
  LEFT JOIN media_items p  ON p.id  = mi.parent_id
  LEFT JOIN media_items gp ON gp.id = p.parent_id
  WHERE wp.user_id = sqlc.arg(user_id)::uuid
    AND wp.occurred_at >= sqlc.arg(since)::timestamptz
    AND wp.occurred_at <  sqlc.arg(until)::timestamptz
    AND mi.type IN ('movie', 'episode', 'track')
    AND (sqlc.narg('library_ids')::uuid[] IS NULL OR mi.library_id = ANY(sqlc.narg('library_ids')::uuid[]))
    AND (sqlc.narg('max_rating_rank')::int IS NULL
         OR content_rating_rank(COALESCE(mi.content_rating, p.content_rating, gp.content_rating)) <= sqlc.narg('max_rating_rank')::int)
    AND media_item_labels_permitted(mi.labels, mi.parent_id, sqlc.narg('blocked_labels')::text[], sqlc.narg('allowed_labels')::text[])
), refs AS (
  SELECT 'movie'::TEXT AS category, id AS ref_id, watched_ms FROM b WHERE type = 'movie'
  UNION ALL
  SELECT 'show'::TEXT, show_id, watched_ms FROM b WHERE type = 'episode' AND show_id IS NOT NULL
  UNION ALL
  SELECT 'album'::TEXT, album_id, watched_ms FROM b WHERE type = 'track' AND album_id IS NOT NULL
  UNION ALL
  SELECT 'artist'::TEXT, artist_id, watched_ms FROM b WHERE type = 'track' AND artist_id IS NOT NULL
), agg AS (
  SELECT category, ref_id, COUNT(*)::BIGINT AS plays, COALESCE(SUM(watched_ms), 0)::BIGINT AS watched_ms
  FROM refs
  GROUP BY category, ref_id
), ranked AS (
  SELECT a.category, a.ref_id, a.plays, a.watched_ms,
         e.title, e.year,
         (COALESCE(e.poster_path, e.thumb_path, ep.poster_path, ep.thumb_path, ''))::TEXT AS poster_path,
         (CASE WHEN a.category = 'album' THEN COALESCE(ep.title, '') ELSE '' END)::TEXT AS parent_title,
         ROW_NUMBER() OVER (PARTITION BY a.category ORDER BY a.plays DESC, a.watched_ms DESC, e.title) AS rn
  FROM agg a
  JOIN media_items e ON e.id = a.ref_id AND e.deleted_at IS NULL
  LEFT JOIN media_items ep ON ep.id = e.parent_id
  WHERE (sqlc.narg('max_rating_rank')::int IS NULL
         OR content_rating_rank(COALESCE(e.content_rating, ep.content_rating)) <= sqlc.narg('max_rating_rank')::int)
    AND media_item_labels_permitted(e.labels, e.parent_id, sqlc.narg('blocked_labels')::text[], sqlc.narg('allowed_labels')::text[])
)
SELECT category, ref_id, plays, watched_ms, title, year, poster_path, parent_title
FROM ranked
WHERE rn <= sqlc.arg(lim)::int
ORDER BY category, rn;

-- name: GetUserStatsByMonth :many
-- Activity per calendar month (YYYY-MM in tz). Sparse: months without plays
-- are absent; the caller fills the gaps.
SELECT to_char(wp.occurred_at AT TIME ZONE sqlc.arg(tz)::TEXT, 'YYYY-MM')::TEXT AS month,
       COUNT(*)::BIGINT AS plays,
       COALESCE(SUM(LEAST(GREATEST(wp.position_ms, 0),
               COALESCE(NULLIF(COALESCE(wp.duration_ms, mi.duration_ms), 0), GREATEST(wp.position_ms, 0)))), 0)::BIGINT AS watched_ms
FROM watch_plays wp
JOIN media_items mi ON mi.id = wp.media_id AND mi.deleted_at IS NULL
LEFT JOIN media_items p  ON p.id  = mi.parent_id
LEFT JOIN media_items gp ON gp.id = p.parent_id
WHERE wp.user_id = sqlc.arg(user_id)::uuid
  AND wp.occurred_at >= sqlc.arg(since)::timestamptz
  AND wp.occurred_at <  sqlc.arg(until)::timestamptz
  AND (sqlc.narg('library_ids')::uuid[] IS NULL OR mi.library_id = ANY(sqlc.narg('library_ids')::uuid[]))
  AND (sqlc.narg('max_rating_rank')::int IS NULL
       OR content_rating_rank(COALESCE(mi.content_rating, p.content_rating, gp.content_rating)) <= sqlc.narg('max_rating_rank')::int)
  AND media_item_labels_permitted(mi.labels, mi.parent_id, sqlc.narg('blocked_labels')::text[], sqlc.narg('allowed_labels')::text[])
GROUP BY 1
ORDER BY 1;

-- name: GetUserStatsHeatmap :many
-- Plays by ISO weekday (1 = Monday .. 7 = Sunday) and hour (0-23) in tz.
-- Sparse, like GetUserStatsByMonth.
SELECT (EXTRACT(ISODOW FROM wp.occurred_at AT TIME ZONE sqlc.arg(tz)::TEXT))::INT AS weekday,
       (EXTRACT(HOUR   FROM wp.occurred_at AT TIME ZONE sqlc.arg(tz)::TEXT))::INT AS hour,
       COUNT(*)::BIGINT AS plays
FROM watch_plays wp
JOIN media_items mi ON mi.id = wp.media_id AND mi.deleted_at IS NULL
LEFT JOIN media_items p  ON p.id  = mi.parent_id
LEFT JOIN media_items gp ON gp.id = p.parent_id
WHERE wp.user_id = sqlc.arg(user_id)::uuid
  AND wp.occurred_at >= sqlc.arg(since)::timestamptz
  AND wp.occurred_at <  sqlc.arg(until)::timestamptz
  AND (sqlc.narg('library_ids')::uuid[] IS NULL OR mi.library_id = ANY(sqlc.narg('library_ids')::uuid[]))
  AND (sqlc.narg('max_rating_rank')::int IS NULL
       OR content_rating_rank(COALESCE(mi.content_rating, p.content_rating, gp.content_rating)) <= sqlc.narg('max_rating_rank')::int)
  AND media_item_labels_permitted(mi.labels, mi.parent_id, sqlc.narg('blocked_labels')::text[], sqlc.narg('allowed_labels')::text[])
GROUP BY 1, 2
ORDER BY 1, 2;

-- name: GetUserStatsDays :many
-- One row per local day with any plays: the caller derives the longest streak
-- and the busiest day from it. At most one row per day of the range.
SELECT (DATE(wp.occurred_at AT TIME ZONE sqlc.arg(tz)::TEXT))::DATE AS day,
       COUNT(*)::BIGINT AS plays,
       COALESCE(SUM(LEAST(GREATEST(wp.position_ms, 0),
               COALESCE(NULLIF(COALESCE(wp.duration_ms, mi.duration_ms), 0), GREATEST(wp.position_ms, 0)))), 0)::BIGINT AS watched_ms
FROM watch_plays wp
JOIN media_items mi ON mi.id = wp.media_id AND mi.deleted_at IS NULL
LEFT JOIN media_items p  ON p.id  = mi.parent_id
LEFT JOIN media_items gp ON gp.id = p.parent_id
WHERE wp.user_id = sqlc.arg(user_id)::uuid
  AND wp.occurred_at >= sqlc.arg(since)::timestamptz
  AND wp.occurred_at <  sqlc.arg(until)::timestamptz
  AND (sqlc.narg('library_ids')::uuid[] IS NULL OR mi.library_id = ANY(sqlc.narg('library_ids')::uuid[]))
  AND (sqlc.narg('max_rating_rank')::int IS NULL
       OR content_rating_rank(COALESCE(mi.content_rating, p.content_rating, gp.content_rating)) <= sqlc.narg('max_rating_rank')::int)
  AND media_item_labels_permitted(mi.labels, mi.parent_id, sqlc.narg('blocked_labels')::text[], sqlc.narg('allowed_labels')::text[])
GROUP BY 1
ORDER BY 1;

-- name: GetUserStatsDevices :many
-- The user's most-used devices (client names such as "Fire TV — Amazon
-- AFTKRT"), by plays.
SELECT COALESCE(NULLIF(wp.client_name, ''), 'Unknown device')::TEXT AS device,
       COUNT(*)::BIGINT AS plays,
       COALESCE(SUM(LEAST(GREATEST(wp.position_ms, 0),
               COALESCE(NULLIF(COALESCE(wp.duration_ms, mi.duration_ms), 0), GREATEST(wp.position_ms, 0)))), 0)::BIGINT AS watched_ms
FROM watch_plays wp
JOIN media_items mi ON mi.id = wp.media_id AND mi.deleted_at IS NULL
LEFT JOIN media_items p  ON p.id  = mi.parent_id
LEFT JOIN media_items gp ON gp.id = p.parent_id
WHERE wp.user_id = sqlc.arg(user_id)::uuid
  AND wp.occurred_at >= sqlc.arg(since)::timestamptz
  AND wp.occurred_at <  sqlc.arg(until)::timestamptz
  AND (sqlc.narg('library_ids')::uuid[] IS NULL OR mi.library_id = ANY(sqlc.narg('library_ids')::uuid[]))
  AND (sqlc.narg('max_rating_rank')::int IS NULL
       OR content_rating_rank(COALESCE(mi.content_rating, p.content_rating, gp.content_rating)) <= sqlc.narg('max_rating_rank')::int)
  AND media_item_labels_permitted(mi.labels, mi.parent_id, sqlc.narg('blocked_labels')::text[], sqlc.narg('allowed_labels')::text[])
GROUP BY 1
ORDER BY plays DESC, watched_ms DESC, 1
LIMIT sqlc.arg(lim)::int;

-- name: GetUserBooksFinished :one
-- Ebooks and comics finished in the range. Reading doesn't produce plays:
-- finishing a book records a manual "watched" mark (MarkItemRead), so this
-- counts those marks. Audiobooks are counted from plays (GetUserStatsTotals).
SELECT COUNT(*)::BIGINT AS books
FROM watch_progress w
JOIN media_items mi ON mi.id = w.media_id AND mi.deleted_at IS NULL
WHERE w.user_id = sqlc.arg(user_id)::uuid
  AND w.mark_state = 'watched'
  AND w.marked_at >= sqlc.arg(since)::timestamptz
  AND w.marked_at <  sqlc.arg(until)::timestamptz
  AND mi.type = 'book'
  AND (sqlc.narg('library_ids')::uuid[] IS NULL OR mi.library_id = ANY(sqlc.narg('library_ids')::uuid[]))
  AND (sqlc.narg('max_rating_rank')::int IS NULL
       OR content_rating_rank(mi.content_rating) <= sqlc.narg('max_rating_rank')::int)
  AND media_item_labels_permitted(mi.labels, mi.parent_id, sqlc.narg('blocked_labels')::text[], sqlc.narg('allowed_labels')::text[]);

-- name: GetUserFirstPlay :one
-- The first thing the user played in the range (Year in review). parent_title
-- is the show for an episode, the artist (or album) for a track; poster_path
-- falls back up the hierarchy like the admin dashboard's top-played list.
SELECT mi.id, mi.type, mi.title, mi.year,
       (COALESCE(gp.title, p.title, ''))::TEXT AS parent_title,
       (COALESCE(gp.poster_path, p.poster_path, mi.poster_path,
                 gp.thumb_path,  p.thumb_path,  mi.thumb_path, ''))::TEXT AS poster_path,
       wp.occurred_at
FROM watch_plays wp
JOIN media_items mi ON mi.id = wp.media_id AND mi.deleted_at IS NULL
LEFT JOIN media_items p  ON p.id  = mi.parent_id
LEFT JOIN media_items gp ON gp.id = p.parent_id
WHERE wp.user_id = sqlc.arg(user_id)::uuid
  AND wp.occurred_at >= sqlc.arg(since)::timestamptz
  AND wp.occurred_at <  sqlc.arg(until)::timestamptz
  AND (sqlc.narg('library_ids')::uuid[] IS NULL OR mi.library_id = ANY(sqlc.narg('library_ids')::uuid[]))
  AND (sqlc.narg('max_rating_rank')::int IS NULL
       OR content_rating_rank(COALESCE(mi.content_rating, p.content_rating, gp.content_rating)) <= sqlc.narg('max_rating_rank')::int)
  AND media_item_labels_permitted(mi.labels, mi.parent_id, sqlc.narg('blocked_labels')::text[], sqlc.narg('allowed_labels')::text[])
ORDER BY wp.occurred_at
LIMIT 1;

-- name: GetUserNewArtists :many
-- Artists the user played for the first time in the range ("new artists
-- discovered"): their first play ever, as far as the play history reaches,
-- falls inside [since, until). Ordered by plays in the range; total is how many
-- there were before the LIMIT.
WITH t AS (
  SELECT CASE WHEN gp.type = 'artist' THEN gp.id WHEN p.type = 'artist' THEN p.id END AS artist_id,
         wp.occurred_at
  FROM watch_plays wp
  JOIN media_items mi ON mi.id = wp.media_id AND mi.deleted_at IS NULL
  LEFT JOIN media_items p  ON p.id  = mi.parent_id
  LEFT JOIN media_items gp ON gp.id = p.parent_id
  WHERE wp.user_id = sqlc.arg(user_id)::uuid
    AND wp.occurred_at < sqlc.arg(until)::timestamptz
    AND mi.type = 'track'
    AND (sqlc.narg('library_ids')::uuid[] IS NULL OR mi.library_id = ANY(sqlc.narg('library_ids')::uuid[]))
    AND (sqlc.narg('max_rating_rank')::int IS NULL
         OR content_rating_rank(COALESCE(mi.content_rating, p.content_rating, gp.content_rating)) <= sqlc.narg('max_rating_rank')::int)
    AND media_item_labels_permitted(mi.labels, mi.parent_id, sqlc.narg('blocked_labels')::text[], sqlc.narg('allowed_labels')::text[])
), firsts AS (
  SELECT artist_id,
         (COUNT(*) FILTER (WHERE occurred_at >= sqlc.arg(since)::timestamptz))::BIGINT AS plays
  FROM t
  WHERE artist_id IS NOT NULL
  GROUP BY artist_id
  HAVING MIN(occurred_at) >= sqlc.arg(since)::timestamptz
)
SELECT e.id, e.title,
       (COALESCE(e.poster_path, e.thumb_path, ''))::TEXT AS poster_path,
       f.plays,
       (COUNT(*) OVER ())::BIGINT AS total
FROM firsts f
JOIN media_items e ON e.id = f.artist_id AND e.deleted_at IS NULL
WHERE (sqlc.narg('max_rating_rank')::int IS NULL
       OR content_rating_rank(e.content_rating) <= sqlc.narg('max_rating_rank')::int)
  AND media_item_labels_permitted(e.labels, e.parent_id, sqlc.narg('blocked_labels')::text[], sqlc.narg('allowed_labels')::text[])
ORDER BY f.plays DESC, e.title
LIMIT sqlc.arg(lim)::int;
