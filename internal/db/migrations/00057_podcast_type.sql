-- +goose Up
-- Podcasts as a first-class library type. v2.0 scope is local-files
-- only: <root>/<Podcast>/<episode>.mp3 — one show per folder, one
-- episode per file. RSS subscription + auto-download is v2.1 work
-- (needs feed poller, retention policy, download queue) and is
-- deliberately not in scope here.
--
-- Item types: 'podcast' for the show, 'podcast_episode' for each
-- episode. Episodes hang off the show as parent, mirroring the
-- artist→track music hierarchy.

ALTER TABLE libraries DROP CONSTRAINT libraries_type_check;
ALTER TABLE libraries ADD CONSTRAINT libraries_type_check
    CHECK (type IN ('movie', 'show', 'music', 'photo', 'dvr', 'audiobook', 'podcast'));

ALTER TABLE media_items DROP CONSTRAINT media_items_type_check;
ALTER TABLE media_items ADD CONSTRAINT media_items_type_check
  CHECK (type IN ('movie','show','season','episode','track','album','artist','photo',
                  'music_video','audiobook','podcast','podcast_episode'));

-- +goose Down
DELETE FROM media_items WHERE type IN ('podcast', 'podcast_episode');
DELETE FROM libraries WHERE type = 'podcast';

ALTER TABLE libraries DROP CONSTRAINT libraries_type_check;
ALTER TABLE libraries ADD CONSTRAINT libraries_type_check
    CHECK (type IN ('movie', 'show', 'music', 'photo', 'dvr', 'audiobook'));

ALTER TABLE media_items DROP CONSTRAINT media_items_type_check;
ALTER TABLE media_items ADD CONSTRAINT media_items_type_check
  CHECK (type IN ('movie','show','season','episode','track','album','artist','photo','music_video','audiobook'));
