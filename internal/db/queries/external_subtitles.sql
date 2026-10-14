-- name: ListExternalSubtitlesForFile :many
SELECT id, file_id, language, title, forced, sdh, source, source_id,
       storage_path, rating, download_count, created_at, offset_ms
FROM external_subtitles
WHERE file_id = $1
ORDER BY language, COALESCE(rating, 0) DESC, created_at DESC;

-- name: GetExternalSubtitle :one
SELECT id, file_id, language, title, forced, sdh, source, source_id,
       storage_path, rating, download_count, created_at, offset_ms
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
          storage_path, rating, download_count, created_at, offset_ms;

-- name: DeleteExternalSubtitle :exec
DELETE FROM external_subtitles WHERE id = $1;

-- name: HasOCRForStream :one
-- Reports whether an OCR'd row already exists for (file, stream). Used by
-- the scheduler's ocr_subtitles sweep to skip files that have already
-- been processed.
SELECT EXISTS(
  SELECT 1 FROM external_subtitles
  WHERE file_id = $1 AND source = 'ocr' AND source_id = $2
);

-- name: SetExternalSubtitleOffset :one
-- Admin timing correction for one external subtitle (see migration 00055).
-- The serve path shifts the cues by it; a re-download of the same release
-- (InsertExternalSubtitle's upsert) keeps it.
UPDATE external_subtitles
SET offset_ms = sqlc.arg('offset_ms')::int
WHERE id = sqlc.arg('id')::uuid
RETURNING id, file_id, language, title, forced, sdh, source, source_id,
          storage_path, rating, download_count, created_at, offset_ms;

-- name: GetSubtitleStreamOffset :one
-- One embedded stream's offset; 0 when none is set.
SELECT COALESCE((SELECT offset_ms FROM subtitle_stream_offsets
                 WHERE file_id = sqlc.arg('file_id')::uuid
                   AND stream_index = sqlc.arg('stream_index')::int), 0)::int AS offset_ms;

-- name: ListSubtitleStreamOffsets :many
-- Every embedded-stream offset on the given files, for item details.
SELECT file_id, stream_index, offset_ms
FROM subtitle_stream_offsets
WHERE file_id = ANY(sqlc.arg('file_ids')::uuid[]);

-- name: UpsertSubtitleStreamOffset :exec
-- Admin timing correction for one embedded subtitle stream (non-zero; a
-- zero offset goes through DeleteSubtitleStreamOffset).
INSERT INTO subtitle_stream_offsets (file_id, stream_index, offset_ms)
VALUES (sqlc.arg('file_id')::uuid, sqlc.arg('stream_index')::int, sqlc.arg('offset_ms')::int)
ON CONFLICT (file_id, stream_index)
DO UPDATE SET offset_ms = EXCLUDED.offset_ms, updated_at = NOW();

-- name: DeleteSubtitleStreamOffset :exec
-- Offset back to 0: the table is sparse.
DELETE FROM subtitle_stream_offsets
WHERE file_id = sqlc.arg('file_id')::uuid AND stream_index = sqlc.arg('stream_index')::int;
