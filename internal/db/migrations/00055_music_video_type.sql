-- +goose Up

-- Add "music_video" as a valid media item type. Music videos sit as
-- children of an artist (no album in between) — a Prince music video
-- collection doesn't have albums the way his discography does, and
-- reusing the artist node lets the web UI surface a "Music Videos"
-- row alongside the albums row on the artist page.
--
-- The scanner routes video files found in a music library to this
-- type instead of treating them as untyped movies, keeping the
-- music library cohesive instead of half-music / half-miscategorized.

ALTER TABLE media_items DROP CONSTRAINT media_items_type_check;
ALTER TABLE media_items ADD CONSTRAINT media_items_type_check
  CHECK (type IN ('movie','show','season','episode','track','album','artist','photo','music_video'));

-- +goose Down

ALTER TABLE media_items DROP CONSTRAINT media_items_type_check;
ALTER TABLE media_items ADD CONSTRAINT media_items_type_check
  CHECK (type IN ('movie','show','season','episode','track','album','artist','photo'));
