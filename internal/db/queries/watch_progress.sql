-- Manual watch marks, Continue Watching dismissals, Next Up, and the
-- per-item watch rollups the library grid shows. Everything here derives
-- state from the user_watch_state view (migration 00023) — never from
-- watch_events directly — so every surface agrees.

-- name: MarkWatchStateForTarget :many
-- Sets the caller's manual played/unplayed mark on one item in a single
-- statement. For a show or season the target expands to every live episode
-- underneath (episode_ancestry) whose own content rating is within the
-- caller's ceiling; for a playable leaf (movie, episode, music video, home
-- video) it is the item itself. The handler has already applied the library
-- ACL and rating ceiling to the target and rejected other types. Marks never
-- write watch_events, so plays and analytics are untouched.
--
-- Rows already carrying the requested mark with no watch activity since it
-- are left alone (and not returned): re-marking them would change nothing but
-- marked_at, and a repeated mark on a long show otherwise rewrote every
-- episode row on every call. A row with events newer than its mark is
-- re-marked even when the state matches, so "mark unwatched" still resets a
-- title the user finished (or started) after last marking it.
--
-- Returns the media ids it marked, which is what a "watched" mark sends on
-- to the user's Trakt history: an item already marked is not sent twice.
WITH targets AS (
    SELECT ea.episode_id AS media_id
    FROM episode_ancestry ea
    WHERE sqlc.arg('target_type')::text IN ('show', 'season')
      AND ea.container_id = sqlc.arg('target_id')::uuid
      AND (sqlc.narg('max_rating_rank')::int IS NULL
           OR content_rating_rank(ea.content_rating) <= sqlc.narg('max_rating_rank')::int)
    UNION ALL
    SELECT m.id AS media_id
    FROM media_items m
    WHERE m.id = sqlc.arg('target_id')::uuid
      AND m.deleted_at IS NULL
      AND m.type IN ('movie', 'episode', 'music_video', 'home_video')
)
INSERT INTO watch_progress (user_id, media_id, mark_state, marked_at)
SELECT sqlc.arg('user_id')::uuid, t.media_id, sqlc.arg('state')::text, now()
FROM targets t
ON CONFLICT (user_id, media_id) DO UPDATE
    SET mark_state = EXCLUDED.mark_state,
        marked_at  = EXCLUDED.marked_at
    WHERE watch_progress.mark_state IS DISTINCT FROM EXCLUDED.mark_state
       OR watch_progress.last_event_at > watch_progress.marked_at
RETURNING watch_progress.media_id;

-- name: DismissContinueWatching :execrows
-- Records a Continue Watching dismissal keyed the way ListContinueWatching
-- rolls tiles up: an episode or season resolves to its show (grandparent,
-- else parent — the same COALESCE the Continue Watching show key uses),
-- anything else to itself. Re-dismissing refreshes dismissed_at. 0 rows
-- means the item does not exist.
INSERT INTO continue_watching_dismissals (user_id, media_id, dismissed_at)
SELECT sqlc.arg('user_id')::uuid,
       CASE
           WHEN m.type = 'episode' THEN COALESCE(gp.id, p.id, m.id)
           WHEN m.type = 'season'  THEN COALESCE(p.id, m.id)
           ELSE m.id
       END,
       now()
FROM media_items m
LEFT JOIN media_items p  ON p.id = m.parent_id
LEFT JOIN media_items gp ON gp.id = p.parent_id
WHERE m.id = sqlc.arg('media_id')::uuid
ON CONFLICT (user_id, media_id) DO UPDATE
    SET dismissed_at = EXCLUDED.dismissed_at;

-- name: ListUpNextEpisodes :many
-- Every live episode of a show (container = the show) or of one season
-- (container = the season), in play order, with the caller's watch state.
-- Episodes over the caller's rating ceiling are left out, so they can never
-- be picked. Flat-layout episodes (no season row) are not included: the Up
-- Next contract always names a season.
SELECT e.id, e.title, e.thumb_path, e.duration_ms,
       e.index AS episode_number,
       se.id AS season_id,
       se.index AS season_number,
       COALESCE(ws.status, 'unwatched')::text AS status,
       COALESCE(ws.position_ms, 0)::bigint AS position_ms,
       COALESCE(ws.resumable, false)::boolean AS resumable,
       ws.last_activity_at
FROM media_items se
JOIN media_items e
  ON e.parent_id = se.id AND e.type = 'episode' AND e.deleted_at IS NULL
LEFT JOIN user_watch_state ws
  ON ws.user_id = sqlc.arg('user_id')::uuid AND ws.media_id = e.id
WHERE se.type = 'season'
  AND se.deleted_at IS NULL
  AND (se.id = sqlc.arg('container_id')::uuid OR se.parent_id = sqlc.arg('container_id')::uuid)
  AND (sqlc.narg('max_rating_rank')::int IS NULL
       OR content_rating_rank(e.content_rating) <= sqlc.narg('max_rating_rank')::int)
ORDER BY COALESCE(se.index, 999999), COALESCE(e.index, 999999), e.sort_title, e.id;

-- name: ListNextUp :many
-- Home "Next Up" row: for each show where the caller has finished at least
-- one episode and has nothing resumable (a resumable show is already on
-- Continue Watching TV — no duplicates), the first not-yet-watched episode
-- after the anchor, where the anchor is the most recently watched episode
-- (ties — e.g. a whole season marked played at once — go to the later
-- episode). Order is season then episode number; season 0 specials are
-- skipped unless the show has no regular season. A show whose next episode
-- was added after the user caught up reappears here. Ordered by the caller's
-- latest activity in the show; shows dismissed (continue_watching_dismissals)
-- after that activity are left out.
--
-- ListUpNextEpisodes + upNextPick (Go) implement the same anchor rule for a
-- single show; keep the two in step.
--
-- The rating ceiling applies to both the show and the episode offered.
-- Library access is filtered by the handler on library_id.
WITH ep AS (
    SELECT se.parent_id AS show_id,
           ws.status,
           ws.resumable,
           ws.last_activity_at,
           COALESCE(se.index, 999999) AS season_key,
           COALESCE(e.index, 999999) AS episode_key,
           (COALESCE(se.index, -1) = 0) AS is_special
    FROM user_watch_state ws
    JOIN media_items e
      ON e.id = ws.media_id AND e.type = 'episode' AND e.deleted_at IS NULL
    JOIN media_items se
      ON se.id = e.parent_id AND se.type = 'season' AND se.deleted_at IS NULL
    WHERE ws.user_id = sqlc.arg('user_id')::uuid
      AND se.parent_id IS NOT NULL
),
shows AS (
    SELECT ep.show_id, max(ep.last_activity_at)::timestamptz AS last_activity
    FROM ep
    GROUP BY ep.show_id
    HAVING bool_or(ep.status = 'watched') AND NOT bool_or(ep.resumable)
),
anchor AS (
    SELECT DISTINCT ON (ep.show_id) ep.show_id, ep.season_key, ep.episode_key
    FROM ep
    WHERE ep.status = 'watched'
    ORDER BY ep.show_id, ep.is_special ASC, ep.last_activity_at DESC,
             ep.season_key DESC, ep.episode_key DESC
)
SELECT nx.id, nx.library_id, nx.title, nx.thumb_path, nx.duration_ms, nx.updated_at,
       nx.season_number, nx.episode_number,
       sh.id          AS show_id,
       sh.title       AS show_title,
       sh.year        AS show_year,
       sh.poster_path AS show_poster_path,
       sh.fanart_path AS show_fanart_path,
       sh.thumb_path  AS show_thumb_path,
       s.last_activity
FROM shows s
JOIN anchor a ON a.show_id = s.show_id
JOIN media_items sh ON sh.id = s.show_id AND sh.deleted_at IS NULL
CROSS JOIN LATERAL (
    SELECT e.id, e.library_id, e.title, e.thumb_path, e.duration_ms, e.updated_at,
           se.index AS season_number, e.index AS episode_number
    FROM media_items se
    JOIN media_items e
      ON e.parent_id = se.id AND e.type = 'episode' AND e.deleted_at IS NULL
    LEFT JOIN user_watch_state ws
      ON ws.user_id = sqlc.arg('user_id')::uuid AND ws.media_id = e.id
    WHERE se.parent_id = s.show_id
      AND se.type = 'season'
      AND se.deleted_at IS NULL
      AND (COALESCE(se.index, 999999), COALESCE(e.index, 999999)) > (a.season_key, a.episode_key)
      AND COALESCE(ws.status, 'unwatched') <> 'watched'
      AND (COALESCE(se.index, -1) <> 0
           OR NOT EXISTS (
               SELECT 1 FROM media_items rs
               WHERE rs.parent_id = s.show_id AND rs.type = 'season'
                 AND rs.deleted_at IS NULL AND COALESCE(rs.index, -1) <> 0))
      AND (sqlc.narg('max_rating_rank')::int IS NULL
           OR content_rating_rank(e.content_rating) <= sqlc.narg('max_rating_rank')::int)
    ORDER BY COALESCE(se.index, 999999), COALESCE(e.index, 999999), e.sort_title, e.id
    LIMIT 1
) nx
WHERE (sqlc.narg('max_rating_rank')::int IS NULL
       OR content_rating_rank(sh.content_rating) <= sqlc.narg('max_rating_rank')::int)
  AND NOT EXISTS (
      SELECT 1 FROM continue_watching_dismissals cwd
      WHERE cwd.user_id = sqlc.arg('user_id')::uuid
        AND cwd.media_id = s.show_id
        AND cwd.dismissed_at >= s.last_activity
  )
ORDER BY s.last_activity DESC
LIMIT sqlc.arg('result_limit')::int;

-- name: ListPlanToWatchHub :many
-- Home "Plan to Watch" row: items the caller set to plan_to_watch (the
-- watching-status list, user_watch_status), most recently set first, minus
-- anything now fully watched (a movie watched, a show with every episode
-- watched). Rating ceiling applied in SQL; library access by the handler.
SELECT mi.id, mi.library_id, mi.type, mi.title, mi.year,
       mi.poster_path, mi.fanart_path, mi.thumb_path, mi.duration_ms, mi.updated_at
FROM user_watch_status uws
JOIN media_items mi ON mi.id = uws.media_item_id AND mi.deleted_at IS NULL
WHERE uws.user_id = sqlc.arg('user_id')::uuid
  AND uws.status = 'plan_to_watch'
  AND (sqlc.narg('max_rating_rank')::int IS NULL
       OR content_rating_rank(mi.content_rating) <= sqlc.narg('max_rating_rank')::int)
  AND media_watch_bucket(sqlc.arg('user_id')::uuid, mi.id, mi.type, sqlc.narg('max_rating_rank')::int) <> 'watched'
ORDER BY uws.updated_at DESC, mi.id
LIMIT sqlc.arg('result_limit')::int;

-- name: ListItemWatchSummaries :many
-- One round trip for a library grid page: the caller's state for each leaf
-- item (status + resume point), and for shows/seasons the number of live
-- episodes within the rating ceiling and how many of them are watched. Leaf
-- rows report leaf_count 0; container rows report status 'unwatched'.
SELECT m.id,
       m.type,
       COALESCE(ws.status, 'unwatched')::text AS status,
       COALESCE(ws.position_ms, 0)::bigint AS position_ms,
       COALESCE(ws.resumable, false)::boolean AS resumable,
       c.leaf_count,
       c.watched_count
FROM media_items m
LEFT JOIN user_watch_state ws
  ON ws.user_id = sqlc.arg('user_id')::uuid AND ws.media_id = m.id
CROSS JOIN LATERAL (
    SELECT count(*)::bigint AS leaf_count,
           (count(*) FILTER (WHERE es.status = 'watched'))::bigint AS watched_count
    FROM episode_ancestry ea
    LEFT JOIN user_watch_state es
      ON es.user_id = sqlc.arg('user_id')::uuid AND es.media_id = ea.episode_id
    WHERE m.type IN ('show', 'season')
      AND ea.container_id = m.id
      AND (sqlc.narg('max_rating_rank')::int IS NULL
           OR content_rating_rank(ea.content_rating) <= sqlc.narg('max_rating_rank')::int)
) c
WHERE m.id = ANY(sqlc.arg('media_ids')::uuid[]);

-- name: PickRandomLibraryItem :one
-- "Surprise me": one random item of the given type from a library, under the
-- same filters as the library grid (ListMediaItemsBy* / CountMediaItemsFiltered)
-- including the rating ceiling and the watch filter. ErrNoRows when nothing
-- matches.
SELECT id, type
FROM media_items
WHERE library_id = sqlc.arg('library_id')::uuid
  AND type = sqlc.arg('item_type')::text
  AND deleted_at IS NULL
  AND (sqlc.narg('genre')::text IS NULL OR sqlc.narg('genre') = ANY(genres))
  AND (sqlc.narg('year_min')::int IS NULL OR year >= sqlc.narg('year_min'))
  AND (sqlc.narg('year_max')::int IS NULL OR year <= sqlc.narg('year_max'))
  AND (sqlc.narg('rating_min')::numeric IS NULL OR rating >= sqlc.narg('rating_min'))
  AND (sqlc.narg('max_rating_rank')::int IS NULL OR content_rating_rank(content_rating) <= sqlc.narg('max_rating_rank'))
  AND (sqlc.narg('watch')::text IS NULL OR media_watch_bucket(sqlc.arg('watch_user_id')::uuid, id, type, sqlc.narg('max_rating_rank')::int) = sqlc.narg('watch')::text)
ORDER BY random()
LIMIT 1;
