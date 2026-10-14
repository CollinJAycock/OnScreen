-- Automatic subtitle download (migration 00050): per-library overrides of the
-- server-wide setting, and the per-(item, language) attempt log that keeps the
-- task from searching the same title on every run.

-- name: ListLibrarySubtitleAutoDownload :many
SELECT library_id, enabled, languages, updated_at
FROM library_subtitle_auto_download;

-- name: GetLibrarySubtitleAutoDownload :one
SELECT library_id, enabled, languages, updated_at
FROM library_subtitle_auto_download
WHERE library_id = $1;

-- name: UpsertLibrarySubtitleAutoDownload :one
INSERT INTO library_subtitle_auto_download (library_id, enabled, languages, updated_at)
VALUES (@library_id, sqlc.narg('enabled'), sqlc.narg('languages'), now())
ON CONFLICT (library_id) DO UPDATE
    SET enabled    = EXCLUDED.enabled,
        languages  = EXCLUDED.languages,
        updated_at = now()
RETURNING library_id, enabled, languages, updated_at;

-- name: DeleteLibrarySubtitleAutoDownload :exec
DELETE FROM library_subtitle_auto_download WHERE library_id = $1;

-- name: ListSubtitleAutoCandidates :many
-- Movies and episodes in the given libraries with at least one wanted
-- language still to look at: never attempted, or a retry / present row whose
-- next_attempt_at has passed. Newest items first, so a fresh import is
-- served before the backlog. The file is the item's best active file (the
-- first part of a stacked movie); episodes carry their show's title and
-- season / episode numbers for the provider search.
SELECT m.id, m.library_id, m.type, m.title, m.year, m.imdb_id, m.tmdb_id,
       m.index AS episode_number,
       season.index AS season_number,
       COALESCE(show.title, season.title, '')::text AS show_title,
       COALESCE(show.imdb_id, season.imdb_id) AS show_imdb_id,
       COALESCE(show.tmdb_id, season.tmdb_id) AS show_tmdb_id,
       f.id AS file_id, f.file_path, f.file_size, f.subtitle_streams
FROM media_items m
JOIN LATERAL (
    SELECT mf.id, mf.file_path, mf.file_size, mf.subtitle_streams
    FROM media_files mf
    WHERE mf.media_item_id = m.id AND mf.status = 'active'
    ORDER BY COALESCE(mf.part_index, 1),
             (mf.resolution_w * mf.resolution_h * COALESCE(mf.bitrate, 0)) DESC NULLS LAST,
             mf.id
    LIMIT 1
) f ON true
LEFT JOIN media_items season ON m.type = 'episode' AND season.id = m.parent_id
LEFT JOIN media_items show ON season.type = 'season' AND show.id = season.parent_id
WHERE m.library_id = ANY(sqlc.arg('library_ids')::uuid[])
  AND m.type IN ('movie', 'episode')
  AND m.deleted_at IS NULL
  AND EXISTS (
      SELECT 1
      FROM unnest(sqlc.arg('languages')::text[]) AS want(code)
      WHERE NOT EXISTS (
          SELECT 1 FROM subtitle_auto_attempts a
          WHERE a.media_id = m.id
            AND a.language = want.code
            AND (a.status IN ('downloaded', 'gave_up')
                 OR a.next_attempt_at IS NULL
                 OR a.next_attempt_at > now())
      )
  )
ORDER BY m.created_at DESC, m.id
LIMIT sqlc.arg('result_limit')::int;

-- name: ListSubtitleAutoAttempts :many
SELECT media_id, language, attempts, status, last_attempt_at, next_attempt_at, last_result
FROM subtitle_auto_attempts
WHERE media_id = ANY(sqlc.arg('media_ids')::uuid[]);

-- name: UpsertSubtitleAutoAttempt :exec
-- Records one look at (item, language). attempts counts provider searches
-- only, so the caller passes the new total.
INSERT INTO subtitle_auto_attempts (media_id, language, attempts, status, last_attempt_at, next_attempt_at, last_result)
VALUES (@media_id, @language, @attempts, @status, now(), sqlc.narg('next_attempt_at'), sqlc.narg('last_result'))
ON CONFLICT (media_id, language) DO UPDATE
    SET attempts        = EXCLUDED.attempts,
        status          = EXCLUDED.status,
        last_attempt_at = now(),
        next_attempt_at = EXCLUDED.next_attempt_at,
        last_result     = EXCLUDED.last_result;

-- name: ListExternalSubtitleLanguagesForFile :many
-- Languages the file already has a downloaded / OCR'd subtitle in.
SELECT DISTINCT language FROM external_subtitles WHERE file_id = $1;
