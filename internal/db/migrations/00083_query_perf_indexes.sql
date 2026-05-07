-- +goose Up
-- Indexes added to back specific slow queries called out in the
-- post-launch DB audit. None of these reference media_items as a
-- cascade target (those are 00081 + 00082); these are purely
-- read-path performance.

-- ListWatchHistory ORDERs (occurred_at DESC) per user and filters
-- event_type IN ('stop','scrobble'). The 00052 partial index
-- covers (event_type, occurred_at DESC) globally — useful for
-- trending — but a per-user history scan still degrades with the
-- user's lifetime stop/scrobble count. Per-user partial fixes that.
CREATE INDEX IF NOT EXISTS idx_watch_events_user_history
    ON watch_events(user_id, occurred_at DESC)
    WHERE event_type IN ('stop', 'scrobble');

-- SearchPeople does LOWER(name) LIKE LOWER(?) || '%'. The existing
-- idx_people_name is btree on LOWER(name) but with the default
-- (collation-aware) opclass, which can't be used for LIKE prefix
-- scans on non-C collations. text_pattern_ops fixes that.
CREATE INDEX IF NOT EXISTS idx_people_name_pattern
    ON people(LOWER(name) text_pattern_ops);

-- GIN trigram indexes for the EXIF search/count queries on photo
-- metadata. pg_trgm is part of the contrib pack so should be
-- available; create the extension if it isn't already.
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX IF NOT EXISTS idx_photo_metadata_camera_make_trgm
    ON photo_metadata USING gin (camera_make gin_trgm_ops)
    WHERE camera_make IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_photo_metadata_camera_model_trgm
    ON photo_metadata USING gin (camera_model gin_trgm_ops)
    WHERE camera_model IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_photo_metadata_lens_model_trgm
    ON photo_metadata USING gin (lens_model gin_trgm_ops)
    WHERE lens_model IS NOT NULL;

-- ListPhotoMapPoints orders by taken_at DESC and takes optional bbox
-- args. The existing (gps_lat, gps_lon) index helps the bbox case
-- but with all-NULL bbox the planner falls back to a seq-scan +
-- sort. Partial index on taken_at restricted to GPS-tagged photos
-- makes the all-bbox-NULL path indexed.
CREATE INDEX IF NOT EXISTS idx_photo_metadata_taken_at_gps
    ON photo_metadata(taken_at DESC)
    WHERE gps_lat IS NOT NULL AND gps_lon IS NOT NULL AND taken_at IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_photo_metadata_taken_at_gps;
DROP INDEX IF EXISTS idx_photo_metadata_lens_model_trgm;
DROP INDEX IF EXISTS idx_photo_metadata_camera_model_trgm;
DROP INDEX IF EXISTS idx_photo_metadata_camera_make_trgm;
DROP INDEX IF EXISTS idx_people_name_pattern;
DROP INDEX IF EXISTS idx_watch_events_user_history;
-- Don't drop the extension — other things may depend on it.
