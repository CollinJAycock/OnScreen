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
