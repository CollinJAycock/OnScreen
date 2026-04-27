-- +goose Up
-- Home videos as a first-class library type. Personal footage —
-- vacations, family events, ranch cams — that has no external metadata
-- agent (TMDB doesn't have your kid's birthday party). v2.1 scope is
-- intentionally narrow: one file = one media_item row, sorted by
-- date-taken in the library landing page.
--
-- The "date taken" reuses the existing `media_items.originally_available_at`
-- column that photos already populate from EXIF DateTimeOriginal at scan
-- time (see ListMediaItemsByTakenAt query). For home videos, the scanner
-- sources the date in priority order:
--   1. ffprobe `creation_time` container tag (set by camcorders / phones)
--   2. file mtime (fallback when no embedded date)
--
-- No new column needed — originally_available_at is already typed as
-- TIMESTAMPTZ and indexed, and the existing taken-at sort path works
-- as-is for any item type that populates it.

ALTER TABLE libraries DROP CONSTRAINT libraries_type_check;
ALTER TABLE libraries ADD CONSTRAINT libraries_type_check
    CHECK (type IN ('movie', 'show', 'music', 'photo', 'dvr', 'audiobook', 'podcast', 'home_video'));

ALTER TABLE media_items DROP CONSTRAINT media_items_type_check;
ALTER TABLE media_items ADD CONSTRAINT media_items_type_check
  CHECK (type IN ('movie','show','season','episode','track','album','artist','photo',
                  'music_video','audiobook','podcast','podcast_episode','home_video'));

-- +goose Down
DELETE FROM media_items WHERE type = 'home_video';
DELETE FROM libraries WHERE type = 'home_video';

ALTER TABLE libraries DROP CONSTRAINT libraries_type_check;
ALTER TABLE libraries ADD CONSTRAINT libraries_type_check
    CHECK (type IN ('movie', 'show', 'music', 'photo', 'dvr', 'audiobook', 'podcast'));

ALTER TABLE media_items DROP CONSTRAINT media_items_type_check;
ALTER TABLE media_items ADD CONSTRAINT media_items_type_check
  CHECK (type IN ('movie','show','season','episode','track','album','artist','photo',
                  'music_video','audiobook','podcast','podcast_episode'));
