-- Franchise collections (migration 00027): TMDB belongs_to_collection
-- groupings materialised as type='franchise' collections. See
-- internal/franchise for the sync rules.

-- name: GetMovieTMDBCollectionLink :one
SELECT media_item_id, tmdb_id, tmdb_collection_id, checked_at
FROM media_item_tmdb_collections
WHERE media_item_id = $1;

-- name: UpsertMovieTMDBCollectionLink :exec
-- Records which TMDB collection a movie belongs to (NULL = none) as of the
-- movie TMDB id it was checked for.
INSERT INTO media_item_tmdb_collections (media_item_id, tmdb_id, tmdb_collection_id, checked_at)
VALUES ($1, $2, $3, NOW())
ON CONFLICT (media_item_id) DO UPDATE
SET tmdb_id = EXCLUDED.tmdb_id,
    tmdb_collection_id = EXCLUDED.tmdb_collection_id,
    checked_at = NOW();

-- name: LinkMoviesToTMDBCollection :exec
-- Parts-driven linking: every live movie whose TMDB id is one of the
-- collection's parts belongs to it. Fills movies never checked, movies
-- checked as "no collection" (TMDB groups a film into a collection only once
-- a sequel exists, so that answer goes stale), and stale re-matched rows. A
-- fresh link to a DIFFERENT collection is left alone: belongs_to_collection is
-- the authoritative per-movie answer and parts lists can lag it.
INSERT INTO media_item_tmdb_collections (media_item_id, tmdb_id, tmdb_collection_id, checked_at)
SELECT mi.id, mi.tmdb_id, sqlc.arg('tmdb_collection_id')::int, NOW()
FROM media_items mi
WHERE mi.type = 'movie'
  AND mi.deleted_at IS NULL
  AND mi.tmdb_id = ANY(sqlc.arg('part_tmdb_ids')::int[])
ON CONFLICT (media_item_id) DO UPDATE
SET tmdb_id = EXCLUDED.tmdb_id,
    tmdb_collection_id = EXCLUDED.tmdb_collection_id,
    checked_at = NOW()
WHERE media_item_tmdb_collections.tmdb_collection_id IS NULL
   OR media_item_tmdb_collections.tmdb_id <> EXCLUDED.tmdb_id;

-- name: ListMoviesNeedingTMDBCollectionCheck :many
-- Backfill work queue: matched movies never checked for a TMDB collection,
-- or checked for a TMDB id they no longer carry (Fix Match). Oldest first so
-- a bounded run makes steady progress through the back catalogue.
SELECT mi.id, mi.tmdb_id
FROM media_items mi
LEFT JOIN media_item_tmdb_collections l ON l.media_item_id = mi.id
WHERE mi.type = 'movie'
  AND mi.deleted_at IS NULL
  AND mi.tmdb_id IS NOT NULL
  AND (l.media_item_id IS NULL OR l.tmdb_id <> mi.tmdb_id)
ORDER BY mi.created_at, mi.id
LIMIT sqlc.arg('lim')::int;

-- name: CountMoviesNeedingTMDBCollectionCheck :one
SELECT COUNT(*)::bigint
FROM media_items mi
LEFT JOIN media_item_tmdb_collections l ON l.media_item_id = mi.id
WHERE mi.type = 'movie'
  AND mi.deleted_at IS NULL
  AND mi.tmdb_id IS NOT NULL
  AND (l.media_item_id IS NULL OR l.tmdb_id <> mi.tmdb_id);

-- name: GetTMDBCollectionSnapshot :one
SELECT tmdb_collection_id, name, overview, poster_url, backdrop_url, parts, fetched_at
FROM tmdb_collections
WHERE tmdb_collection_id = $1;

-- name: UpsertTMDBCollectionSnapshot :exec
INSERT INTO tmdb_collections (tmdb_collection_id, name, overview, poster_url, backdrop_url, parts, fetched_at)
VALUES ($1, $2, $3, $4, $5, $6, NOW())
ON CONFLICT (tmdb_collection_id) DO UPDATE
SET name = EXCLUDED.name,
    overview = EXCLUDED.overview,
    poster_url = EXCLUDED.poster_url,
    backdrop_url = EXCLUDED.backdrop_url,
    parts = EXCLUDED.parts,
    fetched_at = NOW();

-- name: InsertTMDBCollectionPlaceholder :exec
-- Name-and-art stand-in from a movie's belongs_to_collection, stored when the
-- /collection fetch failed on first sight so the franchise can still be
-- named. fetched_at = epoch marks it for the next fetch attempt (and puts it
-- first in the stale-refresh queue); a real snapshot always wins.
INSERT INTO tmdb_collections (tmdb_collection_id, name, poster_url, backdrop_url, parts, fetched_at)
VALUES ($1, $2, $3, $4, '[]'::jsonb, 'epoch'::timestamptz)
ON CONFLICT (tmdb_collection_id) DO NOTHING;

-- name: ListStaleFranchiseSnapshots :many
-- Snapshots of live franchise collections older than the cutoff, oldest
-- first — the maintenance task re-fetches a bounded batch per run so new
-- sequels show up as requestable parts.
SELECT tc.tmdb_collection_id
FROM tmdb_collections tc
JOIN collections c ON c.tmdb_collection_id = tc.tmdb_collection_id AND c.type = 'franchise'
WHERE tc.fetched_at < sqlc.arg('older_than')::timestamptz
ORDER BY tc.fetched_at
LIMIT sqlc.arg('lim')::int;

-- name: ListTMDBCollectionPosters :many
SELECT tmdb_collection_id, poster_url
FROM tmdb_collections
WHERE tmdb_collection_id = ANY(sqlc.arg('ids')::int[]);

-- name: CountFranchiseMembers :one
-- Distinct movies (by TMDB id — the same film in a 4K and a 1080p library is
-- one part, not two) currently linked to the collection. mi.tmdb_id =
-- l.tmdb_id drops links left stale by a re-match.
SELECT COUNT(DISTINCT mi.tmdb_id)::bigint
FROM media_item_tmdb_collections l
JOIN media_items mi ON mi.id = l.media_item_id
WHERE l.tmdb_collection_id = $1
  AND mi.type = 'movie'
  AND mi.deleted_at IS NULL
  AND mi.tmdb_id = l.tmdb_id;

-- name: UpsertFranchiseCollection :one
-- One row per TMDB collection (partial unique index from 00027), so two
-- sibling movies enriched concurrently converge on the same collection.
-- updated_at only moves when the name/description actually changed.
INSERT INTO collections (name, description, type, tmdb_collection_id)
VALUES (sqlc.arg('name'), sqlc.narg('description'), 'franchise', sqlc.arg('tmdb_collection_id')::int)
ON CONFLICT (tmdb_collection_id) WHERE tmdb_collection_id IS NOT NULL
DO UPDATE SET
    name = EXCLUDED.name,
    description = EXCLUDED.description,
    updated_at = CASE
        WHEN collections.name IS DISTINCT FROM EXCLUDED.name
          OR collections.description IS DISTINCT FROM EXCLUDED.description
        THEN NOW() ELSE collections.updated_at END
RETURNING id;

-- name: SyncFranchiseCollectionItems :exec
-- Replaces the collection's membership with the movies currently linked to
-- the TMDB collection, positioned in release order. One statement, so a
-- concurrent reader sees the old or the new membership, never a half-synced
-- one.
WITH members AS (
    SELECT mi.id,
           (ROW_NUMBER() OVER (
                ORDER BY mi.originally_available_at NULLS LAST, mi.year NULLS LAST,
                         mi.sort_title, mi.id) - 1)::int AS pos
    FROM media_item_tmdb_collections l
    JOIN media_items mi ON mi.id = l.media_item_id
    WHERE l.tmdb_collection_id = sqlc.arg('tmdb_collection_id')::int
      AND mi.type = 'movie'
      AND mi.deleted_at IS NULL
      AND mi.tmdb_id = l.tmdb_id
), removed AS (
    DELETE FROM collection_items ci
    WHERE ci.collection_id = sqlc.arg('collection_id')::uuid
      AND NOT EXISTS (SELECT 1 FROM members m WHERE m.id = ci.media_item_id)
)
INSERT INTO collection_items (collection_id, media_item_id, position)
SELECT sqlc.arg('collection_id')::uuid, m.id, m.pos FROM members m
ON CONFLICT (collection_id, media_item_id) DO UPDATE
SET position = EXCLUDED.position
WHERE collection_items.position IS DISTINCT FROM EXCLUDED.position;

-- name: DeleteFranchiseCollection :exec
DELETE FROM collections WHERE type = 'franchise' AND tmdb_collection_id = $1;

-- name: ListFranchiseSyncCandidates :many
-- Every TMDB collection that has a franchise row (may need removing) or at
-- least two linked movies (may need creating). The reconcile pass re-syncs
-- each — DB-only, no TMDB traffic unless a snapshot is missing.
SELECT c.tmdb_collection_id::int AS tmdb_collection_id
FROM collections c
WHERE c.type = 'franchise' AND c.tmdb_collection_id IS NOT NULL
UNION
SELECT l.tmdb_collection_id::int
FROM media_item_tmdb_collections l
WHERE l.tmdb_collection_id IS NOT NULL
GROUP BY l.tmdb_collection_id
HAVING COUNT(*) >= 2;

-- name: ListVisibleFranchiseCollectionIDs :many
-- Franchise collections with at least one item the caller may see. The
-- collections listing drops the rest so a restricted profile isn't handed
-- franchise names from libraries it has no grant on, or above its ceiling.
SELECT DISTINCT ci.collection_id
FROM collection_items ci
JOIN collections c ON c.id = ci.collection_id
JOIN media_items mi ON mi.id = ci.media_item_id
WHERE c.type = 'franchise'
  AND mi.deleted_at IS NULL
  AND (sqlc.narg('library_ids')::uuid[] IS NULL
       OR mi.library_id = ANY(sqlc.narg('library_ids')::uuid[]))
  AND (sqlc.narg('max_rating_rank')::int IS NULL
       OR content_rating_rank(mi.content_rating) <= sqlc.narg('max_rating_rank')::int);

-- name: ListLibraryFranchiseCollections :many
-- The library page's Collections tab: franchise collections with at least one
-- visible item in this library, with that visible count and a fallback cover
-- (the first visible member's poster) for collections without TMDB art.
SELECT c.id, c.name, c.type, c.tmdb_collection_id, tc.poster_url,
       COUNT(*)::bigint AS item_count,
       COALESCE((ARRAY_AGG(mi.poster_path ORDER BY ci.position, ci.media_item_id)
           FILTER (WHERE mi.poster_path IS NOT NULL))[1], '')::text AS first_poster_path
FROM collections c
JOIN collection_items ci ON ci.collection_id = c.id
JOIN media_items mi ON mi.id = ci.media_item_id
LEFT JOIN tmdb_collections tc ON tc.tmdb_collection_id = c.tmdb_collection_id
WHERE c.type = 'franchise'
  AND mi.library_id = sqlc.arg('library_id')::uuid
  AND mi.deleted_at IS NULL
  AND (sqlc.narg('max_rating_rank')::int IS NULL
       OR content_rating_rank(mi.content_rating) <= sqlc.narg('max_rating_rank')::int)
GROUP BY c.id, tc.poster_url
ORDER BY c.name, c.id;

-- name: GetFranchiseForMediaItem :one
-- The franchise collection a movie belongs to, with its stored parts, for the
-- movie page's "Part of…" reference.
SELECT c.id, c.name, c.tmdb_collection_id, tc.parts
FROM collection_items ci
JOIN collections c ON c.id = ci.collection_id
LEFT JOIN tmdb_collections tc ON tc.tmdb_collection_id = c.tmdb_collection_id
WHERE ci.media_item_id = $1 AND c.type = 'franchise'
ORDER BY c.created_at
LIMIT 1;
