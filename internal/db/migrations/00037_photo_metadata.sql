-- +goose Up
-- Per-photo EXIF data extracted at scan time. Lives in its own table so the
-- hot media_items row stays narrow and so we can store the parsed pieces
-- (date taken, camera, GPS) as proper columns alongside a raw_exif JSONB
-- catch-all for everything else.
CREATE TABLE photo_metadata (
    item_id          UUID PRIMARY KEY REFERENCES media_items(id) ON DELETE CASCADE,
    taken_at         TIMESTAMPTZ,
    camera_make      TEXT,
    camera_model     TEXT,
    lens_model       TEXT,
    focal_length_mm  DOUBLE PRECISION,
    aperture         DOUBLE PRECISION,
    shutter_speed    TEXT,
    iso              INT,
    flash            BOOLEAN,
    orientation      INT,
    width            INT,
    height           INT,
    gps_lat          DOUBLE PRECISION,
    gps_lon          DOUBLE PRECISION,
    gps_alt          DOUBLE PRECISION,
    raw_exif         JSONB,
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Date-taken sort drives the photo browse grid; keep a partial index so the
-- common "ORDER BY taken_at DESC" path stays cheap even with millions of rows.
CREATE INDEX idx_photo_metadata_taken_at
    ON photo_metadata(taken_at DESC)
    WHERE taken_at IS NOT NULL;

-- Bounding-box queries for "photos near here" use both coordinates together.
CREATE INDEX idx_photo_metadata_gps
    ON photo_metadata(gps_lat, gps_lon)
    WHERE gps_lat IS NOT NULL AND gps_lon IS NOT NULL;

-- +goose Down
DROP TABLE photo_metadata;
