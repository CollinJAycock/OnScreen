-- name: ListIntroMarkersByMedia :many
-- Returns all markers (intro + credits) for a given media item. The player
-- calls this on item load so it can show Skip Intro / Skip Credits at the
-- right times.
SELECT id, media_item_id, kind, start_ms, end_ms, source, created_at, updated_at
FROM intro_markers
WHERE media_item_id = $1
ORDER BY start_ms;

-- name: ListIntroMarkersBySeason :many
-- Bulk lookup used by the detector so it can skip episodes that already
-- have a manual marker (manual > auto, never overwrite).
SELECT im.id, im.media_item_id, im.kind, im.start_ms, im.end_ms,
       im.source, im.created_at, im.updated_at
FROM intro_markers im
JOIN media_items ep ON ep.id = im.media_item_id
WHERE ep.parent_id = $1;

-- name: UpsertIntroMarker :one
-- Replaces any prior marker of the same (media_item_id, kind). Callers
-- must enforce the manual-wins-over-auto rule above this query.
INSERT INTO intro_markers (media_item_id, kind, start_ms, end_ms, source)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (media_item_id, kind) DO UPDATE
SET start_ms = EXCLUDED.start_ms,
    end_ms = EXCLUDED.end_ms,
    source = EXCLUDED.source,
    updated_at = NOW()
RETURNING id, media_item_id, kind, start_ms, end_ms, source, created_at, updated_at;

-- name: DeleteIntroMarker :exec
DELETE FROM intro_markers
WHERE media_item_id = $1 AND kind = $2;

-- name: DeleteIntroMarkersByMedia :exec
DELETE FROM intro_markers WHERE media_item_id = $1;

-- name: GetIntroMarkerSource :one
-- Used by the auto-detector to check "is there already a manual marker
-- here?" before overwriting.
SELECT source FROM intro_markers
WHERE media_item_id = $1 AND kind = $2;

-- name: ListSeasonsNeedingIntroDetection :many
-- Seasons with at least one file-bearing, non-deleted episode that still
-- lacks an intro marker (any source). DetectLibrary scopes its pass to these
-- so a full-library scan doesn't re-fingerprint the tens of thousands of
-- episodes already marked. Seasons where detection legitimately found no
-- intro re-appear each pass (nothing was written for them) — a small tail
-- compared to the marked majority.
SELECT DISTINCT s.id
FROM media_items s
JOIN media_items ep ON ep.parent_id = s.id AND ep.deleted_at IS NULL
JOIN media_files mf ON mf.media_item_id = ep.id AND mf.status = 'active'
WHERE s.library_id = $1
  AND s.type = 'season'
  AND s.deleted_at IS NULL
  AND NOT EXISTS (
    SELECT 1 FROM intro_markers im
    WHERE im.media_item_id = ep.id AND im.kind = 'intro'
  )
ORDER BY s.id;
