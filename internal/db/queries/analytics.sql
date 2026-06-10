-- Analytics dashboard aggregates. All eight run in parallel from
-- AnalyticsHandler.Get and are memoized server-side (per display timezone)
-- for analyticsCacheTTL.

-- name: GetAnalyticsOverview :one
-- Counts and sums route through watch_plays so scrobble+stop pairs from one
-- session don't double-count. The matview applies a 30-min dedupe per
-- (user, media) — see migrations 00053 / 00004.
SELECT
  (SELECT COUNT(*)                     FROM media_items WHERE deleted_at IS NULL)::BIGINT AS total_items,
  (SELECT COUNT(*)                     FROM media_files WHERE status = 'active')::BIGINT  AS total_files,
  (SELECT COALESCE(SUM(file_size), 0)  FROM media_files WHERE status = 'active')::BIGINT  AS total_size_bytes,
  (SELECT COUNT(*)                     FROM watch_plays)::BIGINT                          AS total_plays,
  (SELECT COALESCE(SUM(duration_ms),0) FROM watch_plays)::BIGINT                          AS total_watch_time_ms;

-- name: GetLibraryAnalytics :many
-- Resolution buckets count only video files: audio tracks and ebooks carry no
-- resolution (previously inflating SD by tens of thousands) and photos carry
-- image dimensions (a tall photo previously counted as "4K").
SELECT
  l.id,
  l.name,
  l.type,
  COUNT(DISTINCT mi.id)::BIGINT                                                       AS item_count,
  COALESCE(SUM(mf.file_size), 0)::BIGINT                                              AS total_size_bytes,
  (COUNT(mf.id) FILTER (WHERE mf.video_codec IS NOT NULL
                          AND mf.resolution_h >= 2160))::BIGINT                       AS res_4k,
  (COUNT(mf.id) FILTER (WHERE mf.video_codec IS NOT NULL
                          AND mf.resolution_h >= 1080 AND mf.resolution_h < 2160))::BIGINT AS res_1080p,
  (COUNT(mf.id) FILTER (WHERE mf.video_codec IS NOT NULL
                          AND mf.resolution_h >= 720  AND mf.resolution_h < 1080))::BIGINT AS res_720p,
  (COUNT(mf.id) FILTER (WHERE mf.video_codec IS NOT NULL
                          AND (mf.resolution_h < 720 OR mf.resolution_h IS NULL)))::BIGINT AS res_sd
FROM libraries l
LEFT JOIN media_items mi ON mi.library_id = l.id AND mi.deleted_at IS NULL
LEFT JOIN media_files mf ON mf.media_item_id = mi.id AND mf.status = 'active'
WHERE l.deleted_at IS NULL
GROUP BY l.id, l.name, l.type
ORDER BY l.name;

-- name: GetVideoCodecBreakdown :many
-- video_codec IS NOT NULL keeps audio/ebook/photo files out of an inflated
-- "unknown" bucket — this panel is specifically about video.
SELECT video_codec::TEXT AS codec, COUNT(*)::BIGINT AS count
FROM media_files
WHERE status = 'active' AND video_codec IS NOT NULL
GROUP BY video_codec
ORDER BY count DESC
LIMIT 10;

-- name: GetContainerBreakdown :many
SELECT COALESCE(container, 'unknown')::TEXT AS container, COUNT(*)::BIGINT AS count
FROM media_files
WHERE status = 'active'
GROUP BY container
ORDER BY count DESC
LIMIT 10;

-- name: GetPlaysPerDay :many
-- Days bucket in the viewer's display timezone (validated IANA name passed by
-- the handler; defaults to UTC) so evening plays don't land on "tomorrow".
-- The +1 day fetch window over-covers the client's local-day fill range.
SELECT (DATE(occurred_at AT TIME ZONE sqlc.arg(tz)::TEXT))::DATE AS date,
       COUNT(*)::BIGINT AS count
FROM watch_plays
WHERE occurred_at >= NOW() - make_interval(days => sqlc.arg(days)::INT + 1)
GROUP BY 1
ORDER BY 1;

-- name: GetBandwidthPerDay :many
-- Estimated bytes streamed: source-file bitrate × watched duration. Prefers
-- the exact file (wp.file_id), falls back to the item's highest-bitrate
-- active file. Transcoded sessions overcount (estimate is at source bitrate).
SELECT (DATE(wp.occurred_at AT TIME ZONE sqlc.arg(tz)::TEXT))::DATE AS date,
       COALESCE(SUM(
         COALESCE(mf_direct.bitrate, mf_any.bitrate)::BIGINT * wp.duration_ms / 8000
       ), 0)::BIGINT AS bytes
FROM watch_plays wp
LEFT JOIN media_files mf_direct
       ON mf_direct.id = wp.file_id AND mf_direct.bitrate IS NOT NULL
LEFT JOIN LATERAL (
  SELECT bitrate FROM media_files
  WHERE media_item_id = wp.media_id AND status = 'active' AND bitrate IS NOT NULL
  ORDER BY bitrate DESC LIMIT 1
) mf_any ON TRUE
WHERE wp.occurred_at >= NOW() - make_interval(days => sqlc.arg(days)::INT + 1)
  AND wp.duration_ms IS NOT NULL
  AND COALESCE(mf_direct.bitrate, mf_any.bitrate) IS NOT NULL
GROUP BY 1
ORDER BY 1;

-- name: GetTopPlayed :many
-- Counts are aggregated per media_id first, then joined to media_items so the
-- poster fallback can reference parent/grandparent without bloating the GROUP
-- BY. poster_path coalesces up the hierarchy — an episode inherits the show's
-- (episode→season→show), a track the album/artist's. parent_title surfaces the
-- show/artist so an episode row can read "Show — Episode" in the UI.
SELECT mi.id, mi.title, mi.year, mi.type,
       (COALESCE(gp.poster_path, p.poster_path, mi.poster_path,
                 gp.thumb_path,  p.thumb_path,  mi.thumb_path, ''))::TEXT AS poster_path,
       (COALESCE(gp.title, p.title, ''))::TEXT                            AS parent_title,
       plays.play_count
FROM (
  SELECT media_id, COUNT(*)::BIGINT AS play_count
  FROM watch_plays
  WHERE occurred_at > NOW() - make_interval(days => sqlc.arg(days)::INT)
  GROUP BY media_id
) plays
JOIN media_items mi ON mi.id = plays.media_id AND mi.deleted_at IS NULL
LEFT JOIN media_items p  ON p.id  = mi.parent_id
LEFT JOIN media_items gp ON gp.id = p.parent_id
ORDER BY plays.play_count DESC, mi.id
LIMIT 10;

-- name: GetRecentPlays :many
-- user_name: this endpoint is admin-only, so showing who played is fine (and
-- is the first thing an admin wants to know). LEFT JOIN — deleted users keep
-- their history rows with a NULL name.
SELECT mi.title, mi.year, mi.type,
       (COALESCE(gp.title, p.title, ''))::TEXT AS parent_title,
       u.username                              AS user_name,
       wp.occurred_at, wp.client_name, wp.duration_ms
FROM watch_plays wp
JOIN media_items mi ON mi.id = wp.media_id
LEFT JOIN media_items p  ON p.id  = mi.parent_id
LEFT JOIN media_items gp ON gp.id = p.parent_id
LEFT JOIN users u ON u.id = wp.user_id
WHERE wp.occurred_at > NOW() - INTERVAL '30 days'
ORDER BY wp.occurred_at DESC
LIMIT 20;

-- name: GetTopUsers :many
-- Per-user leaderboard over the selected window. LEFT JOIN — a deleted user's
-- plays still count, under an empty name the handler renders as "Deleted user".
SELECT wp.user_id,
       COALESCE(u.username, '')::TEXT       AS username,
       COUNT(*)::BIGINT                     AS play_count,
       COALESCE(SUM(wp.duration_ms), 0)::BIGINT AS watch_time_ms
FROM watch_plays wp
LEFT JOIN users u ON u.id = wp.user_id
WHERE wp.occurred_at > NOW() - make_interval(days => sqlc.arg(days)::INT)
GROUP BY wp.user_id, u.username
ORDER BY watch_time_ms DESC, play_count DESC
LIMIT 10;

-- name: GetClientBreakdown :many
-- Plays by client app over the selected window ("Fire TV — Amazon AFTKRT",
-- "Web — Chrome on Windows", ...). Tells the admin which client apps are
-- actually in use.
SELECT COALESCE(client_name, 'Unknown')::TEXT AS client,
       COUNT(*)::BIGINT AS count
FROM watch_plays
WHERE occurred_at > NOW() - make_interval(days => sqlc.arg(days)::INT)
GROUP BY client_name
ORDER BY count DESC
LIMIT 10;

-- name: GetPlaysByHour :many
-- Plays bucketed by local hour of day (viewer timezone) over the selected
-- window — "when is the server busy". Sparse: hours with no plays are absent;
-- the client fills 0..23.
SELECT (EXTRACT(HOUR FROM occurred_at AT TIME ZONE sqlc.arg(tz)::TEXT))::INT AS hour,
       COUNT(*)::BIGINT AS count
FROM watch_plays
WHERE occurred_at > NOW() - make_interval(days => sqlc.arg(days)::INT)
GROUP BY 1
ORDER BY 1;

-- name: GetCompletionStats :one
-- Of plays whose media has a known runtime, how many reached >= 90% (the
-- watched threshold used across the product)? Position is the play's final
-- position; media duration comes from the item, not the play.
SELECT COUNT(*)::BIGINT AS plays_with_duration,
       (COUNT(*) FILTER (WHERE wp.position_ms >= mi.duration_ms * 9 / 10))::BIGINT AS completed
FROM watch_plays wp
JOIN media_items mi ON mi.id = wp.media_id
WHERE wp.occurred_at > NOW() - make_interval(days => sqlc.arg(days)::INT)
  AND mi.duration_ms IS NOT NULL AND mi.duration_ms > 0;

-- name: GetStreamTypesPerDay :many
-- Direct-vs-transcode load over time (viewer-timezone days). decision is
-- collapsed client-side; rows written before the decision column (or by
-- clients that don't send it yet) report as 'unknown'.
SELECT (DATE(occurred_at AT TIME ZONE sqlc.arg(tz)::TEXT))::DATE AS date,
       COALESCE(decision, 'unknown')::TEXT AS decision,
       COUNT(*)::BIGINT AS count
FROM watch_plays
WHERE occurred_at >= NOW() - make_interval(days => sqlc.arg(days)::INT + 1)
GROUP BY 1, 2
ORDER BY 1, 2;
