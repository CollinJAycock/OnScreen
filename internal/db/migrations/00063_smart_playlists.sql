-- +goose Up
-- Smart playlists: rule-based collections evaluated at query time. The
-- rules JSONB drives a filter against media_items, so the playlist
-- always reflects current library state — newly-imported items
-- matching the rules show up immediately, deleted items disappear.
--
-- Rule shape (Go side: SmartPlaylistRules in playlists_smart.go):
--
--   {
--     "type": ["movie", "episode"],
--     "genres": ["Action", "Sci-Fi"],
--     "year_min": 2010,
--     "year_max": 2024,
--     "rating_min": 7.0,
--     "sort": "rating",         // title | year | rating | created_at
--     "sort_dir": "desc",
--     "limit": 50
--   }
--
-- Stored on the same collections table that backs static playlists,
-- with type='smart_playlist'. NULL rules = legacy/static playlist; a
-- non-NULL rules + smart_playlist type = the dynamic kind.
--
-- v2.1 Stage 1 deliberately limits the rule grammar to the filters
-- already supported by the library items endpoint — no full
-- expression language, no nested AND/OR groups. That structure
-- keeps the evaluator a thin layer over the existing query path
-- and makes the visual rule builder (deferred to v2.2) straight-
-- forward to design later.

ALTER TABLE collections DROP CONSTRAINT collections_type_check;
ALTER TABLE collections ADD CONSTRAINT collections_type_check
    CHECK (type IN ('auto_genre', 'playlist', 'smart_playlist'));

ALTER TABLE collections ADD COLUMN rules JSONB;

-- +goose Down
ALTER TABLE collections DROP COLUMN IF EXISTS rules;

DELETE FROM collections WHERE type = 'smart_playlist';

ALTER TABLE collections DROP CONSTRAINT collections_type_check;
ALTER TABLE collections ADD CONSTRAINT collections_type_check
    CHECK (type IN ('auto_genre', 'playlist'));
