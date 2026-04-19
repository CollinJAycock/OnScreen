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

-- name: FindTopLevelItemByTitleYear :one
-- Direct equality lookup matching the unique partial index
-- idx_media_items_library_type_title_year. Used by the scanner's hierarchy
-- find-or-create path so fuzzy full-text search can't miss a show whose
-- title is also present in episode filenames (which would otherwise crowd
-- the LIMITed SearchMediaItems result set).
SELECT id, library_id, type, title, sort_title, original_title, year,
       summary, tagline, rating, audience_rating, content_rating, duration_ms,
       genres, tags, tmdb_id, tvdb_id, imdb_id, musicbrainz_id,
       parent_id, index, poster_path, fanart_path, thumb_path,
       originally_available_at, created_at, updated_at, deleted_at
FROM media_items
WHERE library_id = $1
  AND type = $2
  AND title = $3
  AND COALESCE(year, 0) = COALESCE(sqlc.narg('year')::int, 0)
  AND parent_id IS NULL
  AND deleted_at IS NULL
LIMIT 1;

-- name: FindTopLevelItemsByTitleFlexible :many
-- Scanner fallback for FindTopLevelItemByTitleYear: matches on title OR
-- original_title (case-insensitive) and ignores year. Used when the scanner
-- has no year (raw filename) but enrichment may have set a year on the
-- existing row, or when enrichment renamed the row to a canonical TMDB
-- title. Caller filters by year as a tiebreaker.
SELECT id, library_id, type, title, sort_title, original_title, year,
       summary, tagline, rating, audience_rating, content_rating, duration_ms,
       genres, tags, tmdb_id, tvdb_id, imdb_id, musicbrainz_id,
       parent_id, index, poster_path, fanart_path, thumb_path,
       originally_available_at, created_at, updated_at, deleted_at
FROM media_items
WHERE library_id = $1
  AND type = $2
  AND parent_id IS NULL
  AND deleted_at IS NULL
  AND (lower(title) = lower($3) OR lower(coalesce(original_title, '')) = lower($3))
ORDER BY (tmdb_id IS NOT NULL OR tvdb_id IS NOT NULL) DESC,
         (poster_path IS NOT NULL) DESC,
         created_at ASC
LIMIT 5;

-- name: ListDuplicateTopLevelItems :many
-- Lists groups of top-level media items (movies, shows) in the same library
-- that share a normalized title. Normalization handles the common duplicate
-- causes observed in real libraries: trailing year ("Family Guy 1999"),
-- apostrophes ("Bob's" vs "Bobs"), colons/hyphens ("Dune: Prophecy" vs
-- "Dune Prophecy"), & vs "and" ("Law & Order" vs "Law and Order"), and
-- HTML-escaped ampersands ("Love &amp; Death" vs "Love & Death").
-- Returns one row per loser with the survivor_id. Survivor is the most
-- enriched row (has external IDs > has poster > has year > oldest). Rows
-- whose year conflicts with the survivor's year are NOT merged, so
-- two distinct shows that happen to share a title (e.g. "Heroes" 2006 and
-- "Heroes" 2024) stay separate.
WITH normalized AS (
    SELECT id, library_id, type, year, tmdb_id, tvdb_id, poster_path, created_at,
           lower(trim(
               regexp_replace(
                 regexp_replace(
                   regexp_replace(
                     regexp_replace(
                       replace(replace(coalesce(NULLIF(original_title, ''), title), '&amp;', '&'), '''', ''),
                       '[\s\-]+[\(\[]?(19|20)\d{2}[\)\]]?\s*$', ''
                     ),
                     '\s+(and|&)\s+', ' ', 'gi'
                   ),
                   '[^a-zA-Z0-9]+', ' ', 'g'
                 ),
                 '\s+', ' ', 'g'
               )
           )) AS norm
    FROM media_items
    WHERE type = $1
      AND parent_id IS NULL
      AND deleted_at IS NULL
      AND (sqlc.narg('library_id')::uuid IS NULL OR library_id = sqlc.narg('library_id'))
),
ranked AS (
    SELECT id, library_id, norm, year,
           FIRST_VALUE(id)   OVER w AS survivor_id,
           FIRST_VALUE(year) OVER w AS survivor_year,
           ROW_NUMBER()      OVER w AS rn
    FROM normalized
    WHERE norm <> ''
    WINDOW w AS (
        PARTITION BY library_id, norm
        ORDER BY (tmdb_id IS NOT NULL OR tvdb_id IS NOT NULL) DESC,
                 (poster_path IS NOT NULL) DESC,
                 (year IS NOT NULL) DESC,
                 created_at ASC,
                 id ASC
    )
)
SELECT id AS loser_id, survivor_id::uuid AS survivor_id
FROM ranked
WHERE rn > 1
  AND (year IS NULL OR survivor_year IS NULL OR year = survivor_year);

-- name: ReparentMediaItem :exec
UPDATE media_items
SET parent_id  = $2,
    updated_at = NOW()
WHERE id = $1;

-- name: ReparentMediaFilesByItem :exec
-- Reassigns every media_file pointing at $1 to point at $2 instead.
-- Used when merging two duplicate episode rows.
UPDATE media_files
SET media_item_id = $2,
    scanned_at    = NOW()
WHERE media_item_id = $1;

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

-- name: ListMediaItemsMissingArt :many
-- Returns top-level items (movies + shows) that have no poster so the
-- maintenance backfill can re-run metadata enrichment on them. Seasons and
-- episodes are excluded — enriching a show cascades down to them.
SELECT id, library_id, type, title, sort_title, original_title, year,
       summary, tagline, rating, audience_rating, content_rating, duration_ms,
       genres, tags, tmdb_id, tvdb_id, imdb_id, musicbrainz_id,
       parent_id, index, poster_path, fanart_path, thumb_path,
       originally_available_at, created_at, updated_at, deleted_at
FROM media_items
WHERE type IN ('movie', 'show')
  AND poster_path IS NULL
  AND deleted_at IS NULL
ORDER BY created_at DESC
LIMIT $1;

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
