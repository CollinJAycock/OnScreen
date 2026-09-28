-- name: ListOwnedEpisodeCountsByShowTMDB :many
-- Season-level TV requests (migration 00028): how many distinct episodes of
-- each season the library holds for the shows with these TMDB ids, counting
-- only episodes with a file on disk. A show present in several libraries (or
-- an episode with several versions) counts each season/episode number once.
-- Episodes hang off a season row (show → season → episode); flat-layout
-- episodes with no season row name no season and are not counted.
--
-- Scoped like ListMediaItemsByTMDBIDs: library_ids / max_rating_rank are the
-- caller's grants and rating ceiling (checked on the show), NULL = admin /
-- unrestricted (the fulfilment reconciler passes NULL for both).
SELECT sh.tmdb_id::int AS tmdb_id,
       se.index::int AS season_number,
       COUNT(DISTINCT e.index)::int AS episode_count
FROM media_items sh
JOIN media_items se
  ON se.parent_id = sh.id AND se.type = 'season' AND se.deleted_at IS NULL
JOIN media_items e
  ON e.parent_id = se.id AND e.type = 'episode' AND e.deleted_at IS NULL
WHERE sh.type = 'show'
  AND sh.parent_id IS NULL
  AND sh.deleted_at IS NULL
  AND sh.tmdb_id = ANY(sqlc.arg('tmdb_ids')::int[])
  AND se.index IS NOT NULL
  AND e.index IS NOT NULL
  AND EXISTS (
      SELECT 1 FROM media_files f
      WHERE f.media_item_id = e.id AND f.status = 'active'
  )
  AND (sqlc.narg('library_ids')::uuid[] IS NULL
       OR sh.library_id = ANY(sqlc.narg('library_ids')::uuid[]))
  AND (sqlc.narg('max_rating_rank')::int IS NULL
       OR content_rating_rank(sh.content_rating) <= sqlc.narg('max_rating_rank')::int)
GROUP BY sh.tmdb_id, se.index
ORDER BY sh.tmdb_id, se.index;

-- name: MarkMediaRequestSeasonAvailable :execrows
-- Records one requested season as complete. Conditional on the season not
-- being recorded yet (and the request still in flight), so of any number of
-- concurrent reconciles that see the season complete exactly one gets
-- rows=1 — and only that one sends the "season now available" notice.
-- updated_at is left alone: it stays the time of the last status change.
UPDATE media_requests
SET seasons_available = (
        SELECT array_agg(s ORDER BY s)
        FROM unnest(array_append(seasons_available, @season::int)) AS s
    )
WHERE id = @id
  AND status IN ('approved', 'downloading', 'failed')
  AND NOT (@season::int = ANY(seasons_available));
