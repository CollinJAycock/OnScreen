-- +goose Up
-- The multi-file audiobook scan (commit 2119cf4) introduced an
-- `audiobook_chapter` child item type for the per-file rows under a
-- multi-file book parent, but the media_items_type_check constraint
-- never picked it up — fresh DBs scanning a multi-file book hit a
-- constraint-violation error on the chapter insert. Existing single-
-- file books were unaffected so the bug stayed latent until the
-- audiobook UI parity wave brought multi-file scans into scope on
-- real installs.
--
-- This migration just folds the missing type into the existing list;
-- no data migration needed.

ALTER TABLE media_items DROP CONSTRAINT media_items_type_check;
ALTER TABLE media_items ADD CONSTRAINT media_items_type_check
  CHECK (type IN ('movie','show','season','episode','track','album','artist','photo',
                  'music_video','audiobook','audiobook_chapter','podcast','podcast_episode',
                  'home_video','book'));

-- +goose Down
-- Drop the chapter rows before the constraint excludes them, otherwise
-- the ALTER fails on existing data.
DELETE FROM media_items WHERE type = 'audiobook_chapter';

ALTER TABLE media_items DROP CONSTRAINT media_items_type_check;
ALTER TABLE media_items ADD CONSTRAINT media_items_type_check
  CHECK (type IN ('movie','show','season','episode','track','album','artist','photo',
                  'music_video','audiobook','podcast','podcast_episode','home_video','book'));
