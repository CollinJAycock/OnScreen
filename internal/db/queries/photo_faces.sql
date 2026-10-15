-- Photo libraries: per-library photo settings, RAW + JPEG pairing lookups
-- for the scanner, faces and people, and memories. Migration 00057.

-- name: GetLibraryPhotoSettings :one
SELECT library_id, raw_jpeg_pairing, updated_at
FROM library_photo_settings
WHERE library_id = $1;

-- name: UpsertLibraryPhotoSettings :one
INSERT INTO library_photo_settings (library_id, raw_jpeg_pairing, updated_at)
VALUES ($1, $2, NOW())
ON CONFLICT (library_id) DO UPDATE SET
    raw_jpeg_pairing = EXCLUDED.raw_jpeg_pairing,
    updated_at       = NOW()
RETURNING library_id, raw_jpeg_pairing, updated_at;

-- name: ListPhotoFilesByPaths :many
-- The indexed photo files among a set of exact paths: the scanner's RAW +
-- JPEG sibling lookup (one folder, one name, every photo extension). Exact
-- paths keep it on the media_files.file_path unique index.
SELECT mf.id, mf.media_item_id, mf.file_path
FROM media_files mf
JOIN media_items mi ON mi.id = mf.media_item_id
WHERE mi.library_id = $1
  AND mi.type = 'photo'
  AND mi.deleted_at IS NULL
  AND mf.status = 'active'
  AND mf.file_path = ANY(@paths::text[]);

-- name: CreatePhotoItem :one
-- A new photo item. Photos are resolved by file path, not title (see
-- migration 00057), so this is a plain insert.
INSERT INTO media_items (library_id, type, title, sort_title)
VALUES ($1, 'photo', $2, $3)
RETURNING id;

-- name: ListPhotoLibraryIDs :many
SELECT id FROM libraries WHERE type = 'photo' AND deleted_at IS NULL ORDER BY name;

-- ── faces ────────────────────────────────────────────────────────────────

-- name: ListPhotoFacesForItem :many
SELECT f.id, f.item_id, f.person_id, f.x, f.y, f.w, f.h, f.score, f.source,
       p.name AS person_name
FROM photo_faces f
LEFT JOIN photo_people p ON p.id = f.person_id
WHERE f.item_id = $1
ORDER BY f.x, f.y;

-- name: GetPhotoFace :one
SELECT id, item_id, library_id, person_id, x, y, w, h, score, source, created_at
FROM photo_faces
WHERE id = $1;

-- name: CreatePhotoFace :one
INSERT INTO photo_faces (item_id, library_id, x, y, w, h, score, source)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING id, item_id, library_id, person_id, x, y, w, h, score, source, created_at;

-- name: DeleteUnnamedAutoPhotoFaces :exec
-- Clears an item's earlier detections before it is detected again. Named
-- faces and hand-drawn ones stay.
DELETE FROM photo_faces
WHERE item_id = $1 AND source = 'auto' AND person_id IS NULL;

-- name: SetPhotoFacePerson :exec
UPDATE photo_faces SET person_id = sqlc.narg('person_id') WHERE id = $1;

-- name: DeletePhotoFace :exec
DELETE FROM photo_faces WHERE id = $1;

-- name: ListPhotosForFaceScan :many
-- Photos the face-detection task hasn't looked at yet, newest first.
SELECT mi.id, mi.library_id
FROM media_items mi
LEFT JOIN photo_face_scans s ON s.item_id = mi.id
WHERE mi.type = 'photo'
  AND mi.deleted_at IS NULL
  AND s.item_id IS NULL
  AND (sqlc.narg('library_id')::uuid IS NULL OR mi.library_id = sqlc.narg('library_id'))
ORDER BY mi.created_at DESC, mi.id
LIMIT $1;

-- name: CountPhotosForFaceScan :one
SELECT COUNT(*)::bigint
FROM media_items mi
LEFT JOIN photo_face_scans s ON s.item_id = mi.id
WHERE mi.type = 'photo'
  AND mi.deleted_at IS NULL
  AND s.item_id IS NULL
  AND (sqlc.narg('library_id')::uuid IS NULL OR mi.library_id = sqlc.narg('library_id'));

-- name: RecordPhotoFaceScan :exec
INSERT INTO photo_face_scans (item_id, library_id, faces, status, scanned_at)
VALUES ($1, $2, $3, $4, NOW())
ON CONFLICT (item_id) DO UPDATE SET
    faces      = EXCLUDED.faces,
    status     = EXCLUDED.status,
    scanned_at = NOW();

-- name: DeletePhotoFacesForLibrary :execrows
-- "Delete all face data": every face (NULL library = every library).
DELETE FROM photo_faces
WHERE sqlc.narg('library_id')::uuid IS NULL OR library_id = sqlc.narg('library_id');

-- name: DeletePhotoPeopleForLibrary :execrows
DELETE FROM photo_people
WHERE sqlc.narg('library_id')::uuid IS NULL OR library_id = sqlc.narg('library_id');

-- name: DeletePhotoFaceScansForLibrary :execrows
DELETE FROM photo_face_scans
WHERE sqlc.narg('library_id')::uuid IS NULL OR library_id = sqlc.narg('library_id');

-- ── people ───────────────────────────────────────────────────────────────

-- name: GetPhotoPerson :one
SELECT id, library_id, name, created_by, created_at, updated_at
FROM photo_people
WHERE id = $1;

-- name: FindOrCreatePhotoPerson :one
-- Naming a face with a name the library already has reuses that person.
INSERT INTO photo_people (library_id, name, created_by)
VALUES ($1, $2, $3)
ON CONFLICT (library_id, lower(name)) DO UPDATE SET updated_at = photo_people.updated_at
RETURNING id, library_id, name, created_by, created_at, updated_at;

-- name: RenamePhotoPerson :one
UPDATE photo_people SET name = $2, updated_at = NOW()
WHERE id = $1
RETURNING id, library_id, name, created_by, created_at, updated_at;

-- name: DeletePhotoPerson :exec
DELETE FROM photo_people WHERE id = $1;

-- name: ListPhotoPeople :many
-- The library's people with the photos the caller may see: the count, and
-- the largest visible face as the cover. People with no visible photo are
-- left out, so a restricted viewer can't learn a name from the list.
SELECT p.id, p.name, c.photo_count,
       cf.id AS cover_face_id, cf.item_id AS cover_item_id,
       cf.x AS cover_x, cf.y AS cover_y, cf.w AS cover_w, cf.h AS cover_h
FROM photo_people p
JOIN LATERAL (
    SELECT COUNT(DISTINCT f.item_id)::bigint AS photo_count
    FROM photo_faces f
    JOIN media_items mi ON mi.id = f.item_id
    WHERE f.person_id = p.id
      AND mi.deleted_at IS NULL
      AND (sqlc.narg('max_rating_rank')::int IS NULL
           OR content_rating_rank(mi.content_rating) <= sqlc.narg('max_rating_rank')::int)
      AND media_item_labels_permitted(mi.labels, mi.parent_id, sqlc.narg('blocked_labels')::text[], sqlc.narg('allowed_labels')::text[])
) c ON c.photo_count > 0
JOIN LATERAL (
    SELECT f.id, f.item_id, f.x, f.y, f.w, f.h
    FROM photo_faces f
    JOIN media_items mi ON mi.id = f.item_id
    WHERE f.person_id = p.id
      AND mi.deleted_at IS NULL
      AND (sqlc.narg('max_rating_rank')::int IS NULL
           OR content_rating_rank(mi.content_rating) <= sqlc.narg('max_rating_rank')::int)
      AND media_item_labels_permitted(mi.labels, mi.parent_id, sqlc.narg('blocked_labels')::text[], sqlc.narg('allowed_labels')::text[])
    ORDER BY f.w * f.h DESC, f.id
    LIMIT 1
) cf ON true
WHERE p.library_id = $1
ORDER BY c.photo_count DESC, lower(p.name);

-- name: ListPhotoPersonItems :many
-- A person's photos the caller may see, newest first.
SELECT mi.id, mi.library_id, mi.title, mi.poster_path, mi.created_at, mi.updated_at,
       pm.taken_at, pm.width, pm.height, pm.orientation
FROM media_items mi
LEFT JOIN photo_metadata pm ON pm.item_id = mi.id
WHERE mi.type = 'photo'
  AND mi.deleted_at IS NULL
  AND EXISTS (SELECT 1 FROM photo_faces f WHERE f.item_id = mi.id AND f.person_id = @person_id::uuid)
  AND (sqlc.narg('max_rating_rank')::int IS NULL
       OR content_rating_rank(mi.content_rating) <= sqlc.narg('max_rating_rank')::int)
  AND media_item_labels_permitted(mi.labels, mi.parent_id, sqlc.narg('blocked_labels')::text[], sqlc.narg('allowed_labels')::text[])
ORDER BY COALESCE(pm.taken_at, mi.created_at) DESC, mi.id DESC
LIMIT $1 OFFSET $2;

-- name: CountPhotoPersonItems :one
SELECT COUNT(*)::bigint
FROM media_items mi
WHERE mi.type = 'photo'
  AND mi.deleted_at IS NULL
  AND EXISTS (SELECT 1 FROM photo_faces f WHERE f.item_id = mi.id AND f.person_id = @person_id::uuid)
  AND (sqlc.narg('max_rating_rank')::int IS NULL
       OR content_rating_rank(mi.content_rating) <= sqlc.narg('max_rating_rank')::int)
  AND media_item_labels_permitted(mi.labels, mi.parent_id, sqlc.narg('blocked_labels')::text[], sqlc.narg('allowed_labels')::text[]);

-- name: ListUnnamedPhotoFaces :many
-- Faces nobody has named yet, on photos the caller may see: the "who is
-- this?" strip on the People tab. Larger, more confident faces first.
SELECT f.id, f.item_id, f.x, f.y, f.w, f.h, f.score
FROM photo_faces f
JOIN media_items mi ON mi.id = f.item_id
WHERE f.library_id = $1
  AND f.person_id IS NULL
  AND mi.deleted_at IS NULL
  AND (sqlc.narg('max_rating_rank')::int IS NULL
       OR content_rating_rank(mi.content_rating) <= sqlc.narg('max_rating_rank')::int)
  AND media_item_labels_permitted(mi.labels, mi.parent_id, sqlc.narg('blocked_labels')::text[], sqlc.narg('allowed_labels')::text[])
ORDER BY f.w * f.h DESC, f.score DESC, f.id
LIMIT $2 OFFSET $3;

-- name: ListPhotoPersonSuggestions :many
-- Matching faces is manual; this narrows the search. Unnamed faces on
-- photos taken within twelve hours of one of the person's photos (the same
-- outing, the same party) are suggested, photos with fewer faces first (a
-- lone face next to the person's photos is most often them), then larger
-- faces. Only the person's latest 500 photos anchor the window.
WITH anchors AS (
    SELECT COALESCE(pm.taken_at, mi.created_at) AS t
    FROM photo_faces pf
    JOIN media_items mi ON mi.id = pf.item_id
    LEFT JOIN photo_metadata pm ON pm.item_id = mi.id
    WHERE pf.person_id = @person_id::uuid AND mi.deleted_at IS NULL
    ORDER BY 1 DESC
    LIMIT 500
)
SELECT f.id, f.item_id, f.x, f.y, f.w, f.h, f.score,
       (SELECT COUNT(*) FROM photo_faces f2 WHERE f2.item_id = f.item_id)::bigint AS faces_on_photo
FROM photo_faces f
JOIN media_items mi ON mi.id = f.item_id
LEFT JOIN photo_metadata pm ON pm.item_id = mi.id
WHERE f.library_id = @library_id::uuid
  AND f.person_id IS NULL
  AND mi.deleted_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM photo_faces f3 WHERE f3.item_id = f.item_id AND f3.person_id = @person_id::uuid)
  AND EXISTS (
      SELECT 1 FROM anchors a
      WHERE COALESCE(pm.taken_at, mi.created_at) BETWEEN a.t - interval '12 hours' AND a.t + interval '12 hours'
  )
  AND (sqlc.narg('max_rating_rank')::int IS NULL
       OR content_rating_rank(mi.content_rating) <= sqlc.narg('max_rating_rank')::int)
  AND media_item_labels_permitted(mi.labels, mi.parent_id, sqlc.narg('blocked_labels')::text[], sqlc.narg('allowed_labels')::text[])
ORDER BY faces_on_photo ASC, f.w * f.h DESC, f.id
LIMIT @lim::int;

-- ── memories ─────────────────────────────────────────────────────────────

-- name: ListPhotoMemoryCandidates :many
-- Photos taken inside any of the given [from, to) windows (one per past
-- year, computed in the server's time zone by the caller), across the
-- given libraries, with the caller's rating ceiling and label restriction.
SELECT mi.id, mi.library_id, mi.title, mi.poster_path, mi.updated_at,
       pm.taken_at, pm.width, pm.height, pm.orientation,
       (SELECT COUNT(*) FROM photo_faces f WHERE f.item_id = mi.id)::int AS face_count
FROM media_items mi
JOIN photo_metadata pm ON pm.item_id = mi.id
WHERE mi.library_id = ANY(@library_ids::uuid[])
  AND mi.type = 'photo'
  AND mi.deleted_at IS NULL
  AND pm.taken_at IS NOT NULL
  AND EXISTS (
      SELECT 1 FROM generate_subscripts(@range_from::timestamptz[], 1) AS i
      WHERE pm.taken_at >= (@range_from::timestamptz[])[i]
        AND pm.taken_at <  (@range_to::timestamptz[])[i]
  )
  AND (sqlc.narg('max_rating_rank')::int IS NULL
       OR content_rating_rank(mi.content_rating) <= sqlc.narg('max_rating_rank')::int)
  AND media_item_labels_permitted(mi.labels, mi.parent_id, sqlc.narg('blocked_labels')::text[], sqlc.narg('allowed_labels')::text[])
ORDER BY pm.taken_at
LIMIT @lim::int;

-- name: CountDatedPhotos :one
-- How many dated photos the given libraries hold: memories widen "today"
-- to ±1 day when this is small.
SELECT COUNT(*)::bigint
FROM media_items mi
JOIN photo_metadata pm ON pm.item_id = mi.id
WHERE mi.library_id = ANY(@library_ids::uuid[])
  AND mi.type = 'photo'
  AND mi.deleted_at IS NULL
  AND pm.taken_at IS NOT NULL;
