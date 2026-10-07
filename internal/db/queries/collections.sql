-- name: ListCollections :many
SELECT id, user_id, name, description, type, genre, poster_path, sort_order, created_at, updated_at, rules, library_id, tmdb_collection_id, item_order, poster_item_id
FROM collections
WHERE user_id IS NULL OR user_id = sqlc.narg('user_id')
ORDER BY sort_order, name;

-- name: GetCollection :one
SELECT id, user_id, name, description, type, genre, poster_path, sort_order, created_at, updated_at, rules, library_id, tmdb_collection_id, item_order, poster_item_id
FROM collections WHERE id = $1;

-- name: CreateCollection :one
-- v2.1 added the `rules` JSONB column for smart playlists. Static
-- collections / playlists pass NULL; smart_playlist rows store the
-- filter shape that resolves at query time.
INSERT INTO collections (user_id, name, description, type, genre, rules)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, user_id, name, description, type, genre, poster_path, sort_order, created_at, updated_at, rules, library_id, tmdb_collection_id, item_order, poster_item_id;

-- name: UpdateCollection :one
UPDATE collections SET name = $2, description = $3, updated_at = NOW()
WHERE id = $1
RETURNING id, user_id, name, description, type, genre, poster_path, sort_order, created_at, updated_at, rules, library_id, tmdb_collection_id, item_order, poster_item_id;

-- name: DeleteCollection :exec
DELETE FROM collections WHERE id = $1;

-- name: ListCollectionItems :many
-- v2.1 Track G item 4: optional max_rating_rank gate filters items
-- whose content_rating ranks above the caller's ceiling. NULL ranks
-- (no max set, no rating recorded) pass through — the same lenient
-- semantics used by every other rating-gated query so that
-- as-yet-uncategorised media isn't hidden by accident.
SELECT mi.id, mi.library_id, mi.type, mi.title, mi.sort_title,
       mi.year, mi.rating,
       COALESCE(grandparent.poster_path, parent.poster_path, mi.poster_path,
                grandparent.thumb_path, parent.thumb_path, mi.thumb_path) AS poster_path,
       mi.duration_ms,
       ci.position, ci.added_at
FROM collection_items ci
JOIN media_items mi ON mi.id = ci.media_item_id
LEFT JOIN media_items parent ON parent.id = mi.parent_id
LEFT JOIN media_items grandparent ON grandparent.id = parent.parent_id
WHERE ci.collection_id = $1 AND mi.deleted_at IS NULL
  AND (sqlc.narg('max_rating_rank')::int IS NULL
       OR content_rating_rank(mi.content_rating) <= sqlc.narg('max_rating_rank')::int)
-- ci.media_item_id is the unique tiebreaker: ci.position is NOT unique per
-- collection (no UNIQUE(collection_id, position); concurrent MAX(position)+1
-- inserts and partial reorders produce ties), and without a tiebreaker OFFSET
-- paging over tied rows skips/duplicates them.
--
-- item_order is a manual collection's listing order: 'release' (oldest first,
-- undated last) or 'title'; anything else ('custom', or "" from callers that
-- have no such setting) keeps the stored positions.
ORDER BY
  CASE WHEN sqlc.arg('item_order')::text = 'release'
       THEN COALESCE(mi.originally_available_at, make_date(COALESCE(mi.year, mi.original_year), 1, 1)) END NULLS LAST,
  CASE WHEN sqlc.arg('item_order')::text = 'title' THEN mi.sort_title END,
  ci.position, ci.media_item_id
-- NULLIF so a zero lim means "no limit" (original behaviour); callers pass a
-- positive cap to bound the result. Avoids a forgotten lim returning 0 rows.
LIMIT NULLIF(sqlc.arg('lim')::int, 0) OFFSET sqlc.arg('off')::int;

-- name: CountCollectionItems :one
-- Backs meta.total for the static collection/playlist listing so a paginating
-- client sees the real collection size, not the current page length.
--
-- The library_ids filter must mirror ListCollectionItems exactly. Without it
-- the paged rows were correctly ACL-filtered (so the list came back empty for
-- a caller with no grant) while the count was computed over the whole
-- collection — which handed that caller the exact item count of a private
-- library's folder. NULL = admin / no filtering.
SELECT COUNT(*) FROM collection_items ci
JOIN media_items mi ON mi.id = ci.media_item_id
WHERE ci.collection_id = $1 AND mi.deleted_at IS NULL
  AND (sqlc.narg('library_ids')::uuid[] IS NULL
       OR mi.library_id = ANY(sqlc.narg('library_ids')::uuid[]))
  AND (sqlc.narg('max_rating_rank')::int IS NULL
       OR content_rating_rank(mi.content_rating) <= sqlc.narg('max_rating_rank')::int);

-- name: ListItemsByGenre :many
-- v2.1 Track G item 4: optional max_rating_rank gate. Backs the
-- "More like /Action" auto-genre row on the home hub, which a kid
-- profile would otherwise see populated with R-rated thrillers.
SELECT id, library_id, type, title, sort_title, year, rating, poster_path, duration_ms, created_at
FROM media_items
WHERE deleted_at IS NULL
  AND sqlc.arg('genre')::text = ANY(genres)
  AND type IN ('movie', 'show')
  AND (sqlc.narg('max_rating_rank')::int IS NULL
       OR content_rating_rank(content_rating) <= sqlc.narg('max_rating_rank')::int)
  AND (sqlc.narg('library_ids')::uuid[] IS NULL
       OR library_id = ANY(sqlc.narg('library_ids')::uuid[]))
-- , id tiebreaker: rating is highly non-unique (many ties + the NULLS-LAST
-- bucket), so without it OFFSET paging skips/duplicates tied rows.
ORDER BY rating DESC NULLS LAST, id
LIMIT sqlc.arg('lim')::int OFFSET sqlc.arg('off')::int;

-- name: CountItemsByGenre :one
-- Mirrors ListItemsByGenre's filter — including the per-user library ACL — so
-- the paginated total matches the rows the user can actually see. Without the
-- same gates the pagination footer would lie ("Page 1 of 12" but only 4 visible).
SELECT COUNT(*) FROM media_items
WHERE deleted_at IS NULL AND sqlc.arg('genre')::text = ANY(genres) AND type IN ('movie', 'show')
  AND (sqlc.narg('max_rating_rank')::int IS NULL
       OR content_rating_rank(content_rating) <= sqlc.narg('max_rating_rank')::int)
  AND (sqlc.narg('library_ids')::uuid[] IS NULL
       OR library_id = ANY(sqlc.narg('library_ids')::uuid[]));

-- name: AddCollectionItem :one
INSERT INTO collection_items (collection_id, media_item_id, position)
VALUES ($1, $2, COALESCE((SELECT MAX(position)+1 FROM collection_items WHERE collection_id = $1), 0))
ON CONFLICT (collection_id, media_item_id) DO NOTHING
RETURNING id, collection_id, media_item_id, position, added_at;

-- name: RemoveCollectionItem :exec
DELETE FROM collection_items WHERE collection_id = $1 AND media_item_id = $2;

-- name: UpsertAutoGenreCollection :one
INSERT INTO collections (name, type, genre)
VALUES ($1, 'auto_genre', $1)
ON CONFLICT DO NOTHING
RETURNING id, user_id, name, description, type, genre, poster_path, sort_order, created_at, updated_at, library_id;

-- name: ListAutoGenreCollections :many
SELECT id, user_id, name, description, type, genre, poster_path, sort_order, created_at, updated_at, rules, library_id, tmdb_collection_id, item_order, poster_item_id
FROM collections
WHERE type = 'auto_genre'
ORDER BY name;

-- name: UpsertEventCollection :one
-- Find-or-create the event_folder collection for (libraryID, name).
-- The home_video scanner calls this once per file under a non-root
-- folder, so the same collection gets reused across scan passes and
-- across concurrent goroutines processing files in the same folder.
-- Idempotent via the partial unique index from migration 00071.
--
-- ON CONFLICT DO UPDATE on a no-op (updated_at = updated_at) lets the
-- RETURNING clause fire for both insert and conflict — a bare DO
-- NOTHING would return no rows on conflict, forcing a separate
-- SELECT round-trip just to fetch the existing id.
INSERT INTO collections (library_id, name, type)
VALUES ($1, $2, 'event_folder')
ON CONFLICT (library_id, name) WHERE type = 'event_folder'
DO UPDATE SET updated_at = collections.updated_at
RETURNING id, library_id, user_id, name, description, type, genre, poster_path, sort_order, created_at, updated_at, rules;

-- name: ListEventCollectionsForLibrary :many
-- Auto-created event collections under one library, used by the
-- home_video library page to render the "Events" shelf above the
-- date-bucketed grid.
SELECT id, library_id, user_id, name, description, type, genre, poster_path, sort_order, created_at, updated_at, rules
FROM collections
WHERE type = 'event_folder' AND library_id = $1
ORDER BY name;

-- ── Manual collections (migration 00037) ─────────────────────────────────────

-- name: CreateManualCollection :one
-- A server-owned (user_id NULL) collection an admin curates.
INSERT INTO collections (name, description, type, item_order)
VALUES ($1, $2, 'manual', $3)
RETURNING id, user_id, name, description, type, genre, poster_path, sort_order, created_at, updated_at, rules, library_id, tmdb_collection_id, item_order, poster_item_id;

-- name: SetCollectionItemOrder :exec
UPDATE collections SET item_order = $2, updated_at = NOW() WHERE id = $1;

-- name: SetCollectionPosterItem :exec
-- NULL goes back to the first member's poster.
UPDATE collections SET poster_item_id = $2, updated_at = NOW() WHERE id = $1;

-- name: ListVisibleManualCollections :many
-- Manual collections with at least one member the caller may see (library
-- grant + rating ceiling; NULL = no filter), with the visible member count and
-- a cover from visible members only: the chosen poster member when it is
-- visible and has a poster, else the first visible member that has one. A
-- restricted caller never learns a collection whose members are all hidden
-- from it, nor sees a hidden member's poster on the cover.
SELECT c.id,
       COUNT(*)::bigint AS item_count,
       COALESCE(
         (ARRAY_AGG(mi.poster_path) FILTER (WHERE mi.id = c.poster_item_id AND mi.poster_path IS NOT NULL))[1],
         (ARRAY_AGG(mi.poster_path ORDER BY ci.position, ci.media_item_id) FILTER (WHERE mi.poster_path IS NOT NULL))[1],
         '')::text AS cover_poster_path
FROM collections c
JOIN collection_items ci ON ci.collection_id = c.id
JOIN media_items mi ON mi.id = ci.media_item_id
WHERE c.type = 'manual'
  AND mi.deleted_at IS NULL
  AND (sqlc.narg('library_ids')::uuid[] IS NULL
       OR mi.library_id = ANY(sqlc.narg('library_ids')::uuid[]))
  AND (sqlc.narg('max_rating_rank')::int IS NULL
       OR content_rating_rank(mi.content_rating) <= sqlc.narg('max_rating_rank')::int)
GROUP BY c.id, c.poster_item_id;

-- name: ListLibraryManualCollections :many
-- The library page's Collections tab: manual collections with at least one
-- visible member in this library, with that count and a cover chosen as in
-- ListVisibleManualCollections (from this library's visible members).
SELECT c.id, c.name, c.type,
       COUNT(*)::bigint AS item_count,
       COALESCE(
         (ARRAY_AGG(mi.poster_path) FILTER (WHERE mi.id = c.poster_item_id AND mi.poster_path IS NOT NULL))[1],
         (ARRAY_AGG(mi.poster_path ORDER BY ci.position, ci.media_item_id) FILTER (WHERE mi.poster_path IS NOT NULL))[1],
         '')::text AS cover_poster_path
FROM collections c
JOIN collection_items ci ON ci.collection_id = c.id
JOIN media_items mi ON mi.id = ci.media_item_id
WHERE c.type = 'manual'
  AND mi.library_id = sqlc.arg('library_id')::uuid
  AND mi.deleted_at IS NULL
  AND (sqlc.narg('max_rating_rank')::int IS NULL
       OR content_rating_rank(mi.content_rating) <= sqlc.narg('max_rating_rank')::int)
GROUP BY c.id, c.name, c.type, c.poster_item_id
ORDER BY c.name, c.id;

-- name: ListManualCollectionsForItem :many
-- The manual collections an item belongs to, for its detail page. The caller
-- already passed the item's own visibility check, and the item is a member,
-- so each of these has a member the caller can see.
SELECT c.id, c.name
FROM collection_items ci
JOIN collections c ON c.id = ci.collection_id
WHERE ci.media_item_id = $1 AND c.type = 'manual'
ORDER BY c.name, c.id;
