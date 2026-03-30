-- +goose Up
-- Mark media_files as deleted when their file_path no longer starts with any
-- of the owning library's current scan_paths. This cleans up stale records
-- that accumulate after a library scan-path is changed or media is relocated.
UPDATE media_files AS mf
SET    status = 'deleted'
WHERE  mf.status = 'active'
  AND  NOT EXISTS (
         SELECT 1
         FROM   media_items  mi
         JOIN   libraries     l  ON l.id = mi.library_id
         JOIN   LATERAL unnest(l.scan_paths) AS sp(path) ON TRUE
         WHERE  mi.id = mf.media_item_id
           AND  starts_with(mf.file_path, sp.path)
       );

-- Soft-delete media_items that now have no active or missing files.
UPDATE media_items
SET    deleted_at = NOW()
WHERE  deleted_at IS NULL
  AND  type NOT IN ('show', 'season')
  AND  id NOT IN (
         SELECT DISTINCT media_item_id
         FROM   media_files
         WHERE  status IN ('active', 'missing')
       );

-- +goose Down
-- Not reversible: deleted status and soft-deletes cannot be safely undone.
