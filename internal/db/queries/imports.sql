-- Imports from a Plex, Jellyfin or Emby server (internal/serverimport).

-- name: CreateImportJob :one
INSERT INTO import_jobs (source_kind, source_url, server_name, dry_run, options, mapping, phase, created_by)
VALUES (@source_kind, @source_url, @server_name, @dry_run, @options, @mapping, @phase, @created_by)
RETURNING *;

-- name: GetImportJob :one
SELECT * FROM import_jobs WHERE id = $1;

-- name: ListImportJobs :many
SELECT * FROM import_jobs ORDER BY created_at DESC LIMIT $1;

-- name: UpdateImportJobProgress :exec
-- A running import's phase and counts; also its heartbeat.
UPDATE import_jobs
SET phase = @phase, stats = @stats, updated_at = now()
WHERE id = @id AND status = 'running';

-- name: FinishImportJob :one
UPDATE import_jobs
SET status = @status, phase = @phase, stats = @stats, unmatched = @unmatched,
    error = sqlc.narg('error'), updated_at = now(), finished_at = now()
WHERE id = @id AND status = 'running'
RETURNING *;

-- name: FailStaleImportJobs :execrows
-- Fails running imports whose heartbeat stopped: the server running them
-- went away (restart, crash) without finishing them.
UPDATE import_jobs
SET status = 'failed', error = 'interrupted: the server running the import stopped',
    updated_at = now(), finished_at = now()
WHERE status = 'running' AND updated_at < @stale_before;

-- name: ListImportCandidateItems :many
-- Movies, shows and other videos that carry an external ID, for matching a
-- source item by TMDB / TVDB / IMDb ID.
SELECT id, type, tmdb_id, tvdb_id, imdb_id, duration_ms
FROM media_items
WHERE deleted_at IS NULL
  AND type IN ('movie', 'show', 'music_video', 'home_video')
  AND (tmdb_id IS NOT NULL OR tvdb_id IS NOT NULL OR imdb_id IS NOT NULL);

-- name: ListImportCandidateEpisodes :many
-- Episodes by show, season and episode number, for matching a source
-- episode through its show's external ID. Episodes stored without a season
-- (a flat show folder) are matched by file path only.
SELECT e.id, sh.id AS show_id, s.index::int AS season_number, e.index::int AS episode_number,
       e.duration_ms
FROM media_items e
JOIN media_items s ON s.id = e.parent_id AND s.type = 'season' AND s.deleted_at IS NULL
JOIN media_items sh ON sh.id = s.parent_id AND sh.deleted_at IS NULL
WHERE e.type = 'episode' AND e.deleted_at IS NULL
  AND e.index IS NOT NULL AND s.index IS NOT NULL;

-- name: ListImportCandidateFiles :many
-- Every active file and its item, for matching a source item by file path.
SELECT f.media_item_id, f.file_path, i.type, f.duration_ms AS file_duration_ms,
       i.duration_ms AS item_duration_ms
FROM media_files f
JOIN media_items i ON i.id = f.media_item_id
WHERE f.status = 'active' AND i.deleted_at IS NULL;

-- name: ImportWatchedMark :execrows
-- Marks an item watched as of when the source server last saw it watched.
-- A newer mark already here (made on OnScreen, or by a later import) wins.
INSERT INTO watch_progress (user_id, media_id, mark_state, marked_at)
VALUES (@user_id, @media_id, 'watched', @marked_at)
ON CONFLICT (user_id, media_id) DO UPDATE
SET mark_state = 'watched', marked_at = EXCLUDED.marked_at
WHERE watch_progress.marked_at IS NULL OR watch_progress.marked_at < EXCLUDED.marked_at;

-- name: ImportWatchEvent :execrows
-- Records an imported play ('scrobble') or resume point ('pause'). The same
-- event from an earlier run of the same source is skipped, so a re-run adds
-- only what's new. The watch_events trigger folds it into watch_progress,
-- where newer activity on OnScreen keeps precedence.
INSERT INTO watch_events (user_id, media_id, event_type, position_ms, duration_ms,
                          client_id, client_name, occurred_at)
SELECT @user_id::uuid, @media_id::uuid, @event_type::text, @position_ms::bigint,
       sqlc.narg('duration_ms')::bigint, @client_id::text, @client_name::text,
       @occurred_at::timestamptz
WHERE NOT EXISTS (
    SELECT 1 FROM watch_events
    WHERE user_id = @user_id::uuid AND media_id = @media_id::uuid
      AND occurred_at = @occurred_at::timestamptz AND client_id = @client_id::text
      AND event_type = @event_type::text
);

-- name: ImportUserRating :execrows
-- A rating the user already gave on OnScreen is kept.
INSERT INTO user_ratings (user_id, media_item_id, score)
VALUES (@user_id, @media_item_id, @score)
ON CONFLICT (user_id, media_item_id) DO NOTHING;

-- name: ImportFavorite :execrows
INSERT INTO user_favorites (user_id, media_id)
VALUES (@user_id, @media_id)
ON CONFLICT (user_id, media_id) DO NOTHING;

-- name: FindOrCreateImportedPlaylist :one
-- The user's playlist for a source playlist: the one an earlier run created,
-- or a new one. created reports which.
INSERT INTO collections (user_id, name, type, source, source_key)
VALUES (@user_id, @name, 'playlist', @source, @source_key)
ON CONFLICT (source, source_key) WHERE source IS NOT NULL
DO UPDATE SET updated_at = collections.updated_at
RETURNING id, (xmax = 0)::boolean AS created;

-- name: ImportPlaylistItem :execrows
-- Appends an item to an imported playlist unless it's already there, so a
-- re-run adds what's missing without undoing the user's own reordering.
INSERT INTO collection_items (collection_id, media_item_id, position)
VALUES (@collection_id, @media_item_id,
        COALESCE((SELECT MAX(position) + 1 FROM collection_items WHERE collection_id = @collection_id), 0))
ON CONFLICT (collection_id, media_item_id) DO NOTHING;
