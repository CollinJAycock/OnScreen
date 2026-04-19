-- name: GetMediaItem :one
SELECT id, library_id, type, title, sort_title, original_title, year,
       summary, tagline, rating, audience_rating, content_rating, duration_ms,
       genres, tags, tmdb_id, tvdb_id, imdb_id, musicbrainz_id,
       parent_id, index, poster_path, fanart_path, thumb_path,
       originally_available_at, created_at, updated_at, deleted_at
FROM media_items
WHERE id = $1 AND deleted_at IS NULL;

-- name: GetMediaItemByTMDBID :one
SELECT id, library_id, type, title, sort_title, original_title, year,
       summary, tagline, rating, audience_rating, content_rating, duration_ms,
       genres, tags, tmdb_id, tvdb_id, imdb_id, musicbrainz_id,
       parent_id, index, poster_path, fanart_path, thumb_path,
       originally_available_at, created_at, updated_at, deleted_at
FROM media_items
WHERE library_id = $1 AND tmdb_id = $2 AND deleted_at IS NULL
LIMIT 1;

-- name: ListMediaItems :many
SELECT id, library_id, type, title, sort_title, original_title, year,
       summary, tagline, rating, audience_rating, content_rating, duration_ms,
       genres, tags, tmdb_id, tvdb_id, imdb_id, musicbrainz_id,
       parent_id, index, poster_path, fanart_path, thumb_path,
       originally_available_at, created_at, updated_at, deleted_at
FROM media_items
WHERE library_id = $1
  AND type = $2
  AND deleted_at IS NULL
ORDER BY sort_title
LIMIT $3 OFFSET $4;

-- name: ListMediaItemChildren :many
SELECT id, library_id, type, title, sort_title, original_title, year,
       summary, tagline, rating, audience_rating, content_rating, duration_ms,
       genres, tags, tmdb_id, tvdb_id, imdb_id, musicbrainz_id,
       parent_id, index, poster_path, fanart_path, thumb_path,
       originally_available_at, created_at, updated_at, deleted_at
FROM media_items
WHERE parent_id = $1 AND deleted_at IS NULL
ORDER BY index
LIMIT 1000;

-- name: CreateMediaItem :one
INSERT INTO media_items (
    library_id, type, title, sort_title, original_title, year,
    summary, tagline, rating, audience_rating, content_rating, duration_ms,
    genres, tags, tmdb_id, tvdb_id, imdb_id,
    parent_id, index,
    poster_path, fanart_path, thumb_path,
    originally_available_at
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9, $10, $11, $12,
    $13, $14, $15, $16, $17,
    $18, $19,
    $20, $21, $22,
    $23
)
RETURNING id, library_id, type, title, sort_title, original_title, year,
          summary, tagline, rating, audience_rating, content_rating, duration_ms,
          genres, tags, tmdb_id, tvdb_id, imdb_id, musicbrainz_id,
          parent_id, index, poster_path, fanart_path, thumb_path,
          originally_available_at, created_at, updated_at, deleted_at;

-- name: UpdateMediaItemMetadata :one
UPDATE media_items
SET title                   = $2,
    sort_title              = $3,
    original_title          = $4,
    year                    = $5,
    summary                 = $6,
    tagline                 = $7,
    rating                  = $8,
    audience_rating         = $9,
    content_rating          = $10,
    duration_ms             = $11,
    genres                  = $12,
    tags                    = $13,
    poster_path             = $14,
    fanart_path             = $15,
    thumb_path              = $16,
    originally_available_at = $17,
    tmdb_id                 = COALESCE($18, tmdb_id),
    tvdb_id                 = COALESCE($19, tvdb_id),
    updated_at              = NOW()
WHERE id = $1 AND deleted_at IS NULL
RETURNING id, library_id, type, title, sort_title, original_title, year,
          summary, tagline, rating, audience_rating, content_rating, duration_ms,
          genres, tags, tmdb_id, tvdb_id, imdb_id, musicbrainz_id,
          parent_id, index, poster_path, fanart_path, thumb_path,
          originally_available_at, created_at, updated_at, deleted_at;

-- name: SoftDeleteMediaItem :exec
UPDATE media_items SET deleted_at = NOW(), updated_at = NOW()
WHERE id = $1;

-- name: SoftDeleteMediaItemsByLibrary :exec
UPDATE media_items SET deleted_at = NOW(), updated_at = NOW()
WHERE library_id = $1 AND deleted_at IS NULL;

-- name: SoftDeleteMediaItemIfAllFilesDeleted :exec
UPDATE media_items
SET deleted_at = NOW(), updated_at = NOW()
WHERE media_items.id = $1
  AND NOT EXISTS (
      SELECT 1 FROM media_files
      WHERE media_files.media_item_id = $1 AND media_files.status != 'deleted'
  );

-- name: CountMediaItems :one
SELECT COUNT(*) FROM media_items
WHERE library_id = $1 AND type = $2 AND deleted_at IS NULL;

-- name: SearchMediaItems :many
SELECT id, library_id, type, title, sort_title, original_title, year,
       summary, tagline, rating, audience_rating, content_rating, duration_ms,
       genres, tags, tmdb_id, tvdb_id, imdb_id, musicbrainz_id,
       parent_id, index, poster_path, fanart_path, thumb_path,
       originally_available_at, created_at, updated_at, deleted_at
FROM media_items
WHERE library_id = $1
  AND deleted_at IS NULL
  AND search_vector @@ plainto_tsquery('english', $2)
  AND (sqlc.narg('max_rating_rank')::int IS NULL OR content_rating_rank(content_rating) <= sqlc.narg('max_rating_rank'))
ORDER BY ts_rank(search_vector, plainto_tsquery('english', $2)) DESC
LIMIT $3;

-- name: SearchMediaItemsGlobal :many
SELECT id, library_id, type, title, sort_title, original_title, year,
       summary, tagline, rating, audience_rating, content_rating, duration_ms,
       genres, tags, tmdb_id, tvdb_id, imdb_id, musicbrainz_id,
       parent_id, index, poster_path, fanart_path, thumb_path,
       originally_available_at, created_at, updated_at, deleted_at
FROM media_items
WHERE deleted_at IS NULL
  AND search_vector @@ plainto_tsquery('english', $1)
  AND (sqlc.narg('max_rating_rank')::int IS NULL OR content_rating_rank(content_rating) <= sqlc.narg('max_rating_rank'))
ORDER BY ts_rank(search_vector, plainto_tsquery('english', $1)) DESC
LIMIT $2;

-- name: ListMediaItemsByTitle :many
SELECT id, library_id, type, title, sort_title, original_title, year,
       summary, tagline, rating, audience_rating, content_rating, duration_ms,
       genres, tags, tmdb_id, tvdb_id, imdb_id, musicbrainz_id,
       parent_id, index, poster_path, fanart_path, thumb_path,
       originally_available_at, created_at, updated_at, deleted_at
FROM media_items
WHERE library_id = $1
  AND type = $2
  AND deleted_at IS NULL
  AND (sqlc.narg('genre')::text IS NULL OR sqlc.narg('genre') = ANY(genres))
  AND (sqlc.narg('year_min')::int IS NULL OR year >= sqlc.narg('year_min'))
  AND (sqlc.narg('year_max')::int IS NULL OR year <= sqlc.narg('year_max'))
  AND (sqlc.narg('rating_min')::numeric IS NULL OR rating >= sqlc.narg('rating_min'))
  AND (sqlc.narg('max_rating_rank')::int IS NULL OR content_rating_rank(content_rating) <= sqlc.narg('max_rating_rank'))
ORDER BY sort_title ASC
LIMIT $3 OFFSET $4;

-- name: ListMediaItemsByTitleDesc :many
SELECT id, library_id, type, title, sort_title, original_title, year,
       summary, tagline, rating, audience_rating, content_rating, duration_ms,
       genres, tags, tmdb_id, tvdb_id, imdb_id, musicbrainz_id,
       parent_id, index, poster_path, fanart_path, thumb_path,
       originally_available_at, created_at, updated_at, deleted_at
FROM media_items
WHERE library_id = $1
  AND type = $2
  AND deleted_at IS NULL
  AND (sqlc.narg('genre')::text IS NULL OR sqlc.narg('genre') = ANY(genres))
  AND (sqlc.narg('year_min')::int IS NULL OR year >= sqlc.narg('year_min'))
  AND (sqlc.narg('year_max')::int IS NULL OR year <= sqlc.narg('year_max'))
  AND (sqlc.narg('rating_min')::numeric IS NULL OR rating >= sqlc.narg('rating_min'))
  AND (sqlc.narg('max_rating_rank')::int IS NULL OR content_rating_rank(content_rating) <= sqlc.narg('max_rating_rank'))
ORDER BY sort_title DESC
LIMIT $3 OFFSET $4;

-- name: ListMediaItemsByYear :many
SELECT id, library_id, type, title, sort_title, original_title, year,
       summary, tagline, rating, audience_rating, content_rating, duration_ms,
       genres, tags, tmdb_id, tvdb_id, imdb_id, musicbrainz_id,
       parent_id, index, poster_path, fanart_path, thumb_path,
       originally_available_at, created_at, updated_at, deleted_at
FROM media_items
WHERE library_id = $1
  AND type = $2
  AND deleted_at IS NULL
  AND (sqlc.narg('genre')::text IS NULL OR sqlc.narg('genre') = ANY(genres))
  AND (sqlc.narg('year_min')::int IS NULL OR year >= sqlc.narg('year_min'))
  AND (sqlc.narg('year_max')::int IS NULL OR year <= sqlc.narg('year_max'))
  AND (sqlc.narg('rating_min')::numeric IS NULL OR rating >= sqlc.narg('rating_min'))
  AND (sqlc.narg('max_rating_rank')::int IS NULL OR content_rating_rank(content_rating) <= sqlc.narg('max_rating_rank'))
ORDER BY year ASC NULLS LAST, sort_title ASC
LIMIT $3 OFFSET $4;

-- name: ListMediaItemsByYearDesc :many
SELECT id, library_id, type, title, sort_title, original_title, year,
       summary, tagline, rating, audience_rating, content_rating, duration_ms,
       genres, tags, tmdb_id, tvdb_id, imdb_id, musicbrainz_id,
       parent_id, index, poster_path, fanart_path, thumb_path,
       originally_available_at, created_at, updated_at, deleted_at
FROM media_items
WHERE library_id = $1
  AND type = $2
  AND deleted_at IS NULL
  AND (sqlc.narg('genre')::text IS NULL OR sqlc.narg('genre') = ANY(genres))
  AND (sqlc.narg('year_min')::int IS NULL OR year >= sqlc.narg('year_min'))
  AND (sqlc.narg('year_max')::int IS NULL OR year <= sqlc.narg('year_max'))
  AND (sqlc.narg('rating_min')::numeric IS NULL OR rating >= sqlc.narg('rating_min'))
  AND (sqlc.narg('max_rating_rank')::int IS NULL OR content_rating_rank(content_rating) <= sqlc.narg('max_rating_rank'))
ORDER BY year DESC NULLS LAST, sort_title ASC
LIMIT $3 OFFSET $4;

-- name: ListMediaItemsByRating :many
SELECT id, library_id, type, title, sort_title, original_title, year,
       summary, tagline, rating, audience_rating, content_rating, duration_ms,
       genres, tags, tmdb_id, tvdb_id, imdb_id, musicbrainz_id,
       parent_id, index, poster_path, fanart_path, thumb_path,
       originally_available_at, created_at, updated_at, deleted_at
FROM media_items
WHERE library_id = $1
  AND type = $2
  AND deleted_at IS NULL
  AND (sqlc.narg('genre')::text IS NULL OR sqlc.narg('genre') = ANY(genres))
  AND (sqlc.narg('year_min')::int IS NULL OR year >= sqlc.narg('year_min'))
  AND (sqlc.narg('year_max')::int IS NULL OR year <= sqlc.narg('year_max'))
  AND (sqlc.narg('rating_min')::numeric IS NULL OR rating >= sqlc.narg('rating_min'))
  AND (sqlc.narg('max_rating_rank')::int IS NULL OR content_rating_rank(content_rating) <= sqlc.narg('max_rating_rank'))
ORDER BY rating DESC NULLS LAST, sort_title ASC
LIMIT $3 OFFSET $4;

-- name: ListMediaItemsByRatingAsc :many
SELECT id, library_id, type, title, sort_title, original_title, year,
       summary, tagline, rating, audience_rating, content_rating, duration_ms,
       genres, tags, tmdb_id, tvdb_id, imdb_id, musicbrainz_id,
       parent_id, index, poster_path, fanart_path, thumb_path,
       originally_available_at, created_at, updated_at, deleted_at
FROM media_items
WHERE library_id = $1
  AND type = $2
  AND deleted_at IS NULL
  AND (sqlc.narg('genre')::text IS NULL OR sqlc.narg('genre') = ANY(genres))
  AND (sqlc.narg('year_min')::int IS NULL OR year >= sqlc.narg('year_min'))
  AND (sqlc.narg('year_max')::int IS NULL OR year <= sqlc.narg('year_max'))
  AND (sqlc.narg('rating_min')::numeric IS NULL OR rating >= sqlc.narg('rating_min'))
  AND (sqlc.narg('max_rating_rank')::int IS NULL OR content_rating_rank(content_rating) <= sqlc.narg('max_rating_rank'))
ORDER BY rating ASC NULLS LAST, sort_title ASC
LIMIT $3 OFFSET $4;

-- name: ListMediaItemsByDateAdded :many
SELECT id, library_id, type, title, sort_title, original_title, year,
       summary, tagline, rating, audience_rating, content_rating, duration_ms,
       genres, tags, tmdb_id, tvdb_id, imdb_id, musicbrainz_id,
       parent_id, index, poster_path, fanart_path, thumb_path,
       originally_available_at, created_at, updated_at, deleted_at
FROM media_items
WHERE library_id = $1
  AND type = $2
  AND deleted_at IS NULL
  AND (sqlc.narg('genre')::text IS NULL OR sqlc.narg('genre') = ANY(genres))
  AND (sqlc.narg('year_min')::int IS NULL OR year >= sqlc.narg('year_min'))
  AND (sqlc.narg('year_max')::int IS NULL OR year <= sqlc.narg('year_max'))
  AND (sqlc.narg('rating_min')::numeric IS NULL OR rating >= sqlc.narg('rating_min'))
  AND (sqlc.narg('max_rating_rank')::int IS NULL OR content_rating_rank(content_rating) <= sqlc.narg('max_rating_rank'))
ORDER BY created_at DESC
LIMIT $3 OFFSET $4;

-- name: ListMediaItemsByDateAddedAsc :many
SELECT id, library_id, type, title, sort_title, original_title, year,
       summary, tagline, rating, audience_rating, content_rating, duration_ms,
       genres, tags, tmdb_id, tvdb_id, imdb_id, musicbrainz_id,
       parent_id, index, poster_path, fanart_path, thumb_path,
       originally_available_at, created_at, updated_at, deleted_at
FROM media_items
WHERE library_id = $1
  AND type = $2
  AND deleted_at IS NULL
  AND (sqlc.narg('genre')::text IS NULL OR sqlc.narg('genre') = ANY(genres))
  AND (sqlc.narg('year_min')::int IS NULL OR year >= sqlc.narg('year_min'))
  AND (sqlc.narg('year_max')::int IS NULL OR year <= sqlc.narg('year_max'))
  AND (sqlc.narg('rating_min')::numeric IS NULL OR rating >= sqlc.narg('rating_min'))
  AND (sqlc.narg('max_rating_rank')::int IS NULL OR content_rating_rank(content_rating) <= sqlc.narg('max_rating_rank'))
ORDER BY created_at ASC
LIMIT $3 OFFSET $4;

-- name: CountMediaItemsFiltered :one
SELECT COUNT(*) FROM media_items
WHERE library_id = $1 AND type = $2 AND deleted_at IS NULL
  AND (sqlc.narg('genre')::text IS NULL OR sqlc.narg('genre') = ANY(genres))
  AND (sqlc.narg('year_min')::int IS NULL OR year >= sqlc.narg('year_min'))
  AND (sqlc.narg('year_max')::int IS NULL OR year <= sqlc.narg('year_max'))
  AND (sqlc.narg('rating_min')::numeric IS NULL OR rating >= sqlc.narg('rating_min'))
  AND (sqlc.narg('max_rating_rank')::int IS NULL OR content_rating_rank(content_rating) <= sqlc.narg('max_rating_rank'));

-- name: ListDistinctGenres :many
SELECT DISTINCT g::text AS genre
FROM media_items, unnest(genres) AS g
WHERE library_id = $1 AND deleted_at IS NULL
ORDER BY genre;

-- name: ListHubRecentlyAdded :many
SELECT library_id, media_id, type, title, year, rating, poster_path, created_at
FROM hub_recently_added
WHERE library_id = $1
ORDER BY created_at DESC
LIMIT $2;

-- name: RefreshHubRecentlyAdded :exec
REFRESH MATERIALIZED VIEW CONCURRENTLY hub_recently_added;

-- name: ListRecentlyAdded :many
SELECT id, library_id, type, title, sort_title, original_title, year,
       summary, tagline, rating, audience_rating, content_rating, duration_ms,
       genres, tags, tmdb_id, tvdb_id, imdb_id, musicbrainz_id,
       parent_id, index, poster_path, fanart_path, thumb_path,
       originally_available_at, created_at, updated_at, deleted_at
FROM media_items
WHERE deleted_at IS NULL
  AND type IN ('movie', 'show')
  AND (sqlc.narg('library_id')::uuid IS NULL OR library_id = sqlc.narg('library_id'))
  AND (sqlc.narg('max_rating_rank')::int IS NULL OR content_rating_rank(content_rating) <= sqlc.narg('max_rating_rank'))
ORDER BY created_at DESC
LIMIT sqlc.arg('limit');

-- name: ListContinueWatching :many
SELECT m.id, m.library_id, m.type, m.title, m.sort_title,
       m.original_title, m.year, m.summary, m.tagline, m.rating, m.audience_rating,
       m.content_rating, m.duration_ms, m.genres, m.tags, m.tmdb_id, m.tvdb_id, m.imdb_id,
       m.musicbrainz_id, m.parent_id, m.index, m.poster_path, m.fanart_path, m.thumb_path,
       m.originally_available_at, m.created_at, m.updated_at, m.deleted_at,
       ws.position_ms AS view_offset, ws.duration_ms AS view_duration,
       COALESCE(grandparent.poster_path, parent.poster_path, m.poster_path, grandparent.thumb_path, parent.thumb_path, m.thumb_path) AS fallback_poster
FROM watch_state ws
JOIN media_items m ON m.id = ws.media_id
LEFT JOIN media_items parent ON parent.id = m.parent_id
LEFT JOIN media_items grandparent ON grandparent.id = parent.parent_id
WHERE ws.user_id = $1
  AND ws.status = 'in_progress'
  AND m.deleted_at IS NULL
  AND (sqlc.narg('max_rating_rank')::int IS NULL OR content_rating_rank(m.content_rating) <= sqlc.narg('max_rating_rank'))
ORDER BY ws.last_watched_at DESC
LIMIT $2;

-- ── Media Files ───────────────────────────────────────────────────────────────

-- name: GetMediaFile :one
SELECT id, media_item_id, file_path, file_size, container, video_codec,
       audio_codec, resolution_w, resolution_h, bitrate, hdr_type, frame_rate,
       audio_streams, subtitle_streams, chapters, file_hash,
       status, missing_since, scanned_at, created_at, duration_ms
FROM media_files
WHERE id = $1;

-- name: GetMediaFileByPath :one
SELECT id, media_item_id, file_path, file_size, container, video_codec,
       audio_codec, resolution_w, resolution_h, bitrate, hdr_type, frame_rate,
       audio_streams, subtitle_streams, chapters, file_hash,
       status, missing_since, scanned_at, created_at, duration_ms
FROM media_files
WHERE file_path = $1;

-- name: GetMediaFileByHash :one
SELECT id, media_item_id, file_path, file_size, container, video_codec,
       audio_codec, resolution_w, resolution_h, bitrate, hdr_type, frame_rate,
       audio_streams, subtitle_streams, chapters, file_hash,
       status, missing_since, scanned_at, created_at, duration_ms
FROM media_files
WHERE file_hash = $1 AND status IN ('missing', 'deleted')
ORDER BY created_at DESC
LIMIT 1;

-- name: ListMediaFilesForItem :many
SELECT id, media_item_id, file_path, file_size, container, video_codec,
       audio_codec, resolution_w, resolution_h, bitrate, hdr_type, frame_rate,
       audio_streams, subtitle_streams, chapters, file_hash,
       status, missing_since, scanned_at, created_at, duration_ms
FROM media_files
WHERE media_item_id = $1
ORDER BY (resolution_w * resolution_h * COALESCE(bitrate, 0)) DESC;  -- best quality first (ADR-031)

-- name: CreateMediaFile :one
INSERT INTO media_files (
    media_item_id, file_path, file_size, container, video_codec,
    audio_codec, resolution_w, resolution_h, bitrate, hdr_type, frame_rate,
    audio_streams, subtitle_streams, chapters, file_hash, duration_ms
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9, $10, $11,
    $12, $13, $14, $15, $16
)
RETURNING id, media_item_id, file_path, file_size, container, video_codec,
          audio_codec, resolution_w, resolution_h, bitrate, hdr_type, frame_rate,
          audio_streams, subtitle_streams, chapters, file_hash,
          status, missing_since, scanned_at, created_at, duration_ms;

-- name: UpdateMediaFilePath :exec
UPDATE media_files
SET file_path     = $2,
    status        = 'active',
    missing_since = NULL,
    scanned_at    = NOW()
WHERE id = $1;

-- name: MarkMediaFileMissing :exec
UPDATE media_files
SET status        = 'missing',
    missing_since = NOW()
WHERE id = $1 AND status = 'active';

-- name: MarkMediaFileActive :exec
UPDATE media_files
SET status        = 'active',
    missing_since = NULL,
    scanned_at    = NOW()
WHERE id = $1;

-- name: MarkMediaFileDeleted :exec
UPDATE media_files
SET status = 'deleted'
WHERE id = $1;

-- name: UpdateMediaFileHash :exec
UPDATE media_files
SET file_hash  = $2,
    scanned_at = NOW()
WHERE id = $1;

-- name: UpdateMediaFileItemID :exec
UPDATE media_files
SET media_item_id = $2,
    scanned_at    = NOW()
WHERE id = $1;

-- name: UpdateMediaFileTechnicalMetadata :exec
UPDATE media_files
SET container        = $2,
    video_codec      = $3,
    audio_codec      = $4,
    resolution_w     = $5,
    resolution_h     = $6,
    bitrate          = $7,
    hdr_type         = $8,
    frame_rate       = $9,
    audio_streams    = $10,
    subtitle_streams = $11,
    chapters         = $12,
    duration_ms      = $13,
    scanned_at       = NOW()
WHERE id = $1;

-- name: ListActiveFilesForLibrary :many
SELECT mf.id, mf.media_item_id, mf.file_path, mf.file_size, mf.container, mf.video_codec,
       mf.audio_codec, mf.resolution_w, mf.resolution_h, mf.bitrate, mf.hdr_type, mf.frame_rate,
       mf.audio_streams, mf.subtitle_streams, mf.chapters, mf.file_hash,
       mf.status, mf.missing_since, mf.scanned_at, mf.created_at, mf.duration_ms
FROM media_files mf
JOIN media_items mi ON mi.id = mf.media_item_id
WHERE mi.library_id = $1 AND mf.status = 'active';

-- name: DeleteMissingFilesByLibrary :exec
UPDATE media_files
SET status = 'deleted'
WHERE status = 'missing'
  AND media_item_id IN (
      SELECT id FROM media_items WHERE library_id = $1 AND deleted_at IS NULL
  );

-- name: SoftDeleteItemsWithNoActiveFiles :exec
-- Soft-delete leaf items (those that own files directly) with no active files.
-- Container types (show, season, artist, album) never own files — they're
-- handled by SoftDeleteEmptyContainerItems instead.
UPDATE media_items
SET deleted_at = NOW(), updated_at = NOW()
WHERE library_id = $1
  AND deleted_at IS NULL
  AND type IN ('movie', 'episode', 'track', 'photo')
  AND NOT EXISTS (
      SELECT 1 FROM media_files
      WHERE media_files.media_item_id = media_items.id AND media_files.status = 'active'
  );

-- name: SoftDeleteEmptyContainerItems :exec
-- Soft-delete container items (show, season, artist, album) whose every
-- child has been soft-deleted. Call twice in sequence to cascade up: the
-- first pass clears empty seasons/albums, the second clears shows/artists
-- whose seasons/albums just died.
UPDATE media_items AS parent
SET deleted_at = NOW(), updated_at = NOW()
WHERE parent.library_id = $1
  AND parent.deleted_at IS NULL
  AND parent.type IN ('show', 'season', 'artist', 'album')
  AND NOT EXISTS (
      SELECT 1 FROM media_items child
      WHERE child.parent_id = parent.id AND child.deleted_at IS NULL
  );

-- name: ListMissingFilesOlderThan :many
SELECT id, media_item_id, file_path, file_size, container, video_codec,
       audio_codec, resolution_w, resolution_h, bitrate, hdr_type, frame_rate,
       audio_streams, subtitle_streams, chapters, file_hash,
       status, missing_since, scanned_at, created_at, duration_ms
FROM media_files
WHERE status = 'missing' AND missing_since < $1
LIMIT 5000;
