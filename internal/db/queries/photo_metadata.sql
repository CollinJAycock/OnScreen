-- name: UpsertPhotoMetadata :exec
-- Replace the per-photo EXIF row in one statement so re-scans don't leave
-- stale partial data behind. updated_at is reset on every write so we can
-- tell whether enrichment ran for the current file revision.
INSERT INTO photo_metadata (
    item_id, taken_at, camera_make, camera_model, lens_model,
    focal_length_mm, aperture, shutter_speed, iso, flash,
    orientation, width, height, gps_lat, gps_lon, gps_alt,
    raw_exif, updated_at
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9, $10,
    $11, $12, $13, $14, $15, $16,
    $17, NOW()
)
ON CONFLICT (item_id) DO UPDATE SET
    taken_at        = EXCLUDED.taken_at,
    camera_make     = EXCLUDED.camera_make,
    camera_model    = EXCLUDED.camera_model,
    lens_model      = EXCLUDED.lens_model,
    focal_length_mm = EXCLUDED.focal_length_mm,
    aperture        = EXCLUDED.aperture,
    shutter_speed   = EXCLUDED.shutter_speed,
    iso             = EXCLUDED.iso,
    flash           = EXCLUDED.flash,
    orientation     = EXCLUDED.orientation,
    width           = EXCLUDED.width,
    height          = EXCLUDED.height,
    gps_lat         = EXCLUDED.gps_lat,
    gps_lon         = EXCLUDED.gps_lon,
    gps_alt         = EXCLUDED.gps_alt,
    raw_exif        = EXCLUDED.raw_exif,
    updated_at      = NOW();

-- name: GetPhotoMetadata :one
SELECT item_id, taken_at, camera_make, camera_model, lens_model,
       focal_length_mm, aperture, shutter_speed, iso, flash,
       orientation, width, height, gps_lat, gps_lon, gps_alt,
       raw_exif, updated_at
FROM photo_metadata
WHERE item_id = $1;

-- name: DeletePhotoMetadata :exec
DELETE FROM photo_metadata WHERE item_id = $1;

-- name: ListPhotosByLibrary :many
-- Returns photo items for a library, joined with photo_metadata and ordered
-- by taken_at when present (with file mtime as fallback so screenshots and
-- EXIF-less PNGs still sort sensibly). Date range is inclusive on both ends;
-- pass NULL for from/to to mean "no bound." Used by /api/v1/photos to back
-- date-bucketed grids.
SELECT
    mi.id, mi.library_id, mi.title, mi.poster_path,
    mi.created_at, mi.updated_at,
    pm.taken_at, pm.camera_make, pm.camera_model,
    pm.width, pm.height, pm.orientation
FROM media_items mi
LEFT JOIN photo_metadata pm ON pm.item_id = mi.id
WHERE mi.library_id = $1
  AND mi.type = 'photo'
  AND mi.deleted_at IS NULL
  AND (sqlc.narg('from')::timestamptz IS NULL OR COALESCE(pm.taken_at, mi.created_at) >= sqlc.narg('from'))
  AND (sqlc.narg('to')::timestamptz   IS NULL OR COALESCE(pm.taken_at, mi.created_at) <= sqlc.narg('to'))
ORDER BY COALESCE(pm.taken_at, mi.created_at) DESC, mi.id DESC
LIMIT $2 OFFSET $3;

-- name: CountPhotosByLibrary :one
-- Companion count for ListPhotosByLibrary so paginated UIs can render a
-- total. Same date-range semantics.
SELECT COUNT(*)
FROM media_items mi
LEFT JOIN photo_metadata pm ON pm.item_id = mi.id
WHERE mi.library_id = $1
  AND mi.type = 'photo'
  AND mi.deleted_at IS NULL
  AND (sqlc.narg('from')::timestamptz IS NULL OR COALESCE(pm.taken_at, mi.created_at) >= sqlc.narg('from'))
  AND (sqlc.narg('to')::timestamptz   IS NULL OR COALESCE(pm.taken_at, mi.created_at) <= sqlc.narg('to'));

-- name: ListPhotoMapPoints :many
-- Returns geotagged photos for a library as (id, lat, lon, taken_at, poster).
-- Bbox params are inclusive and optional — pass NULL for any of min/max
-- lat/lon to mean "no bound on this edge." Caller is responsible for
-- handling the antimeridian (west > east) by issuing two queries.
-- Hard cap on rows protects the wire from the worst case (200k geotagged
-- photos in one library) — clients are expected to either zoom in to
-- narrow the bbox or accept truncation at the limit.
SELECT
    mi.id, mi.library_id, mi.title, mi.poster_path,
    pm.gps_lat, pm.gps_lon,
    pm.taken_at, mi.created_at
FROM media_items mi
JOIN photo_metadata pm ON pm.item_id = mi.id
WHERE mi.library_id = $1
  AND mi.type = 'photo'
  AND mi.deleted_at IS NULL
  AND pm.gps_lat IS NOT NULL
  AND pm.gps_lon IS NOT NULL
  AND (sqlc.narg('min_lat')::double precision IS NULL OR pm.gps_lat >= sqlc.narg('min_lat'))
  AND (sqlc.narg('max_lat')::double precision IS NULL OR pm.gps_lat <= sqlc.narg('max_lat'))
  AND (sqlc.narg('min_lon')::double precision IS NULL OR pm.gps_lon >= sqlc.narg('min_lon'))
  AND (sqlc.narg('max_lon')::double precision IS NULL OR pm.gps_lon <= sqlc.narg('max_lon'))
ORDER BY COALESCE(pm.taken_at, mi.created_at) DESC, mi.id
LIMIT $2;

-- name: CountPhotoMapPoints :one
-- Total geotagged photos in the library (ignoring bbox) so the client can
-- show "showing 5000 of 23107 — zoom in to see more" and decide whether
-- to bail on rendering.
SELECT COUNT(*)::bigint
FROM media_items mi
JOIN photo_metadata pm ON pm.item_id = mi.id
WHERE mi.library_id = $1
  AND mi.type = 'photo'
  AND mi.deleted_at IS NULL
  AND pm.gps_lat IS NOT NULL
  AND pm.gps_lon IS NOT NULL;

-- name: SearchPhotosByExif :many
-- Filter photos by EXIF metadata. Every filter is optional — pass NULL to
-- skip it. Text fields use case-insensitive substring match (ILIKE %term%)
-- so "sony" matches both "SONY" and "Sony Group Corporation". Numeric
-- ranges are inclusive on both ends. has_gps=true requires both lat and
-- lon present; has_gps=false requires either to be null. NULL (unset)
-- means "don't filter on GPS." INNER JOIN on photo_metadata since every
-- non-trivial filter touches an EXIF field — photos without EXIF rows
-- would never match anyway.
SELECT
    mi.id, mi.library_id, mi.title, mi.poster_path,
    mi.created_at, mi.updated_at,
    pm.taken_at, pm.camera_make, pm.camera_model,
    pm.lens_model, pm.focal_length_mm, pm.aperture, pm.iso,
    pm.width, pm.height, pm.orientation,
    pm.gps_lat, pm.gps_lon
FROM media_items mi
JOIN photo_metadata pm ON pm.item_id = mi.id
WHERE mi.library_id = $1
  AND mi.type = 'photo'
  AND mi.deleted_at IS NULL
  AND (sqlc.narg('camera_make')::text   IS NULL OR pm.camera_make  ILIKE '%' || sqlc.narg('camera_make')::text  || '%')
  AND (sqlc.narg('camera_model')::text  IS NULL OR pm.camera_model ILIKE '%' || sqlc.narg('camera_model')::text || '%')
  AND (sqlc.narg('lens_model')::text    IS NULL OR pm.lens_model   ILIKE '%' || sqlc.narg('lens_model')::text   || '%')
  AND (sqlc.narg('aperture_min')::double precision IS NULL OR pm.aperture        >= sqlc.narg('aperture_min'))
  AND (sqlc.narg('aperture_max')::double precision IS NULL OR pm.aperture        <= sqlc.narg('aperture_max'))
  AND (sqlc.narg('iso_min')::int                   IS NULL OR pm.iso             >= sqlc.narg('iso_min'))
  AND (sqlc.narg('iso_max')::int                   IS NULL OR pm.iso             <= sqlc.narg('iso_max'))
  AND (sqlc.narg('focal_min')::double precision    IS NULL OR pm.focal_length_mm >= sqlc.narg('focal_min'))
  AND (sqlc.narg('focal_max')::double precision    IS NULL OR pm.focal_length_mm <= sqlc.narg('focal_max'))
  AND (sqlc.narg('from')::timestamptz IS NULL OR pm.taken_at >= sqlc.narg('from'))
  AND (sqlc.narg('to')::timestamptz   IS NULL OR pm.taken_at <= sqlc.narg('to'))
  AND (sqlc.narg('has_gps')::boolean IS NULL
       OR (sqlc.narg('has_gps')::boolean = true  AND pm.gps_lat IS NOT NULL AND pm.gps_lon IS NOT NULL)
       OR (sqlc.narg('has_gps')::boolean = false AND (pm.gps_lat IS NULL OR pm.gps_lon IS NULL)))
ORDER BY COALESCE(pm.taken_at, mi.created_at) DESC, mi.id DESC
LIMIT $2 OFFSET $3;

-- name: CountPhotosByExif :one
-- Companion count for SearchPhotosByExif so paginated UIs can render a
-- total. Same filter semantics.
SELECT COUNT(*)
FROM media_items mi
JOIN photo_metadata pm ON pm.item_id = mi.id
WHERE mi.library_id = $1
  AND mi.type = 'photo'
  AND mi.deleted_at IS NULL
  AND (sqlc.narg('camera_make')::text   IS NULL OR pm.camera_make  ILIKE '%' || sqlc.narg('camera_make')::text  || '%')
  AND (sqlc.narg('camera_model')::text  IS NULL OR pm.camera_model ILIKE '%' || sqlc.narg('camera_model')::text || '%')
  AND (sqlc.narg('lens_model')::text    IS NULL OR pm.lens_model   ILIKE '%' || sqlc.narg('lens_model')::text   || '%')
  AND (sqlc.narg('aperture_min')::double precision IS NULL OR pm.aperture        >= sqlc.narg('aperture_min'))
  AND (sqlc.narg('aperture_max')::double precision IS NULL OR pm.aperture        <= sqlc.narg('aperture_max'))
  AND (sqlc.narg('iso_min')::int                   IS NULL OR pm.iso             >= sqlc.narg('iso_min'))
  AND (sqlc.narg('iso_max')::int                   IS NULL OR pm.iso             <= sqlc.narg('iso_max'))
  AND (sqlc.narg('focal_min')::double precision    IS NULL OR pm.focal_length_mm >= sqlc.narg('focal_min'))
  AND (sqlc.narg('focal_max')::double precision    IS NULL OR pm.focal_length_mm <= sqlc.narg('focal_max'))
  AND (sqlc.narg('from')::timestamptz IS NULL OR pm.taken_at >= sqlc.narg('from'))
  AND (sqlc.narg('to')::timestamptz   IS NULL OR pm.taken_at <= sqlc.narg('to'))
  AND (sqlc.narg('has_gps')::boolean IS NULL
       OR (sqlc.narg('has_gps')::boolean = true  AND pm.gps_lat IS NOT NULL AND pm.gps_lon IS NOT NULL)
       OR (sqlc.narg('has_gps')::boolean = false AND (pm.gps_lat IS NULL OR pm.gps_lon IS NULL)));

-- name: ListPhotoTimelineBuckets :many
-- Returns photo counts per (year, month) for a library. Used by the timeline
-- sidebar — clicking "March 2024" jumps the grid to the first photo of
-- that month. COALESCE(taken_at, created_at) keeps screenshots in the
-- timeline at their import date.
SELECT
    EXTRACT(YEAR  FROM COALESCE(pm.taken_at, mi.created_at))::int AS year,
    EXTRACT(MONTH FROM COALESCE(pm.taken_at, mi.created_at))::int AS month,
    COUNT(*)::bigint AS count
FROM media_items mi
LEFT JOIN photo_metadata pm ON pm.item_id = mi.id
WHERE mi.library_id = $1
  AND mi.type = 'photo'
  AND mi.deleted_at IS NULL
GROUP BY year, month
ORDER BY year DESC, month DESC;
