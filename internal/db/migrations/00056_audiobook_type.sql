-- +goose Up
-- Audiobooks become a first-class library and media_items type. Scope
-- for v2.0 is intentionally narrow: one audiobook file = one
-- media_items row, author lives on the item as metadata (not a
-- separate hierarchy node), chapter navigation comes from ffprobe-
-- detected chapter markers at playback time. Richer structure
-- (author/series pages, multi-file books) lands in v2.1.

ALTER TABLE libraries DROP CONSTRAINT libraries_type_check;
ALTER TABLE libraries ADD CONSTRAINT libraries_type_check
    CHECK (type IN ('movie', 'show', 'music', 'photo', 'dvr', 'audiobook'));

ALTER TABLE media_items DROP CONSTRAINT media_items_type_check;
ALTER TABLE media_items ADD CONSTRAINT media_items_type_check
  CHECK (type IN ('movie','show','season','episode','track','album','artist','photo','music_video','audiobook'));

-- +goose Down
DELETE FROM media_items WHERE type = 'audiobook';
DELETE FROM libraries WHERE type = 'audiobook';

ALTER TABLE libraries DROP CONSTRAINT libraries_type_check;
ALTER TABLE libraries ADD CONSTRAINT libraries_type_check
    CHECK (type IN ('movie', 'show', 'music', 'photo', 'dvr'));

ALTER TABLE media_items DROP CONSTRAINT media_items_type_check;
ALTER TABLE media_items ADD CONSTRAINT media_items_type_check
  CHECK (type IN ('movie','show','season','episode','track','album','artist','photo','music_video'));
