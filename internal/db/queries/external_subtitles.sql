-- name: ListExternalSubtitlesForFile :many
SELECT id, file_id, language, title, forced, sdh, source, source_id,
       storage_path, rating, download_count, created_at
FROM external_subtitles
WHERE file_id = $1
ORDER BY language, COALESCE(rating, 0) DESC, created_at DESC;

-- name: GetExternalSubtitle :one
SELECT id, file_id, language, title, forced, sdh, source, source_id,
       storage_path, rating, download_count, created_at
FROM external_subtitles
WHERE id = $1;

-- name: InsertExternalSubtitle :one
INSERT INTO external_subtitles (
    file_id, language, title, forced, sdh,
    source, source_id, storage_path, rating, download_count
) VALUES (
    @file_id, @language, @title, @forced, @sdh,
    @source, @source_id, @storage_path, @rating, @download_count
)
ON CONFLICT (file_id, source, source_id)
DO UPDATE SET
    language       = EXCLUDED.language,
    title          = EXCLUDED.title,
    forced         = EXCLUDED.forced,
    sdh            = EXCLUDED.sdh,
    storage_path   = EXCLUDED.storage_path,
    rating         = EXCLUDED.rating,
    download_count = EXCLUDED.download_count
RETURNING id, file_id, language, title, forced, sdh, source, source_id,
          storage_path, rating, download_count, created_at;

-- name: DeleteExternalSubtitle :exec
DELETE FROM external_subtitles WHERE id = $1;
