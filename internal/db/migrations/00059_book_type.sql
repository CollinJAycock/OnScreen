-- +goose Up
-- Books / comics as a first-class library type. v2.1 Stage 1 scope is
-- CBZ-only — CBZ files are just zip archives with images inside, so
-- support comes from the Go stdlib (archive/zip) without any new
-- dependency. CBR (RAR-based) and EPUB land in v2.x once we pick the
-- parser deps:
--
--   CBR:  github.com/nwaples/rardecode (no cgo, MIT) is the standard
--         choice; smaller than github.com/saracen/go-rar but ~95% of
--         CBRs are uncompressed-or-RAR4 so the smaller dep covers them.
--   EPUB: github.com/taylorskalyo/goreader/epub or building on top of
--         go-shiori/go-epub. Decision deferred to the EPUB-spike commit.
--
-- One file = one media_item row (flat model, like audiobooks). Page
-- count comes from probing the zip directory at scan time and stored
-- on media_items.duration_ms (re-purposed: "duration" for a book
-- means total pages). This keeps the schema cost zero — the field
-- was already there for movies/episodes/tracks and was always nullable.
-- A dedicated page_count column would be cleaner but adds an
-- ALTER TABLE we don't need yet.

ALTER TABLE libraries DROP CONSTRAINT libraries_type_check;
ALTER TABLE libraries ADD CONSTRAINT libraries_type_check
    CHECK (type IN ('movie', 'show', 'music', 'photo', 'dvr', 'audiobook', 'podcast', 'home_video', 'book'));

ALTER TABLE media_items DROP CONSTRAINT media_items_type_check;
ALTER TABLE media_items ADD CONSTRAINT media_items_type_check
  CHECK (type IN ('movie','show','season','episode','track','album','artist','photo',
                  'music_video','audiobook','podcast','podcast_episode','home_video','book'));

-- +goose Down
DELETE FROM media_items WHERE type = 'book';
DELETE FROM libraries WHERE type = 'book';

ALTER TABLE libraries DROP CONSTRAINT libraries_type_check;
ALTER TABLE libraries ADD CONSTRAINT libraries_type_check
    CHECK (type IN ('movie', 'show', 'music', 'photo', 'dvr', 'audiobook', 'podcast', 'home_video'));

ALTER TABLE media_items DROP CONSTRAINT media_items_type_check;
ALTER TABLE media_items ADD CONSTRAINT media_items_type_check
  CHECK (type IN ('movie','show','season','episode','track','album','artist','photo',
                  'music_video','audiobook','podcast','podcast_episode','home_video'));
