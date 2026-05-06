-- +goose Up
-- Subtype column for media_items. Used today by the show-scanner to
-- mark OVAs / ONAs / specials so they sit alongside main-run
-- episodes in the same season grid without polluting the absolute-
-- numbering sequence. Null for regular episodes and non-episode
-- types (movie, track, photo, etc.).
--
-- Convention for episode rows:
--   'ova'     — Original Video Animation (anime)
--   'ona'     — Original Net Animation (anime, web release)
--   'special' — special episode / extra (TMDB season 0 convention)
--   'movie'   — theatrical anime film embedded in a TV series
--   'pv'      — promotional video / music video for the series
--   'oad'     — Original Animation DVD (a kind of OVA)
--    null    — regular numbered episode
--
-- Open-set TEXT (no CHECK constraint) so the scanner can introduce
-- new kinds without a schema migration. The UI groups null vs
-- non-null rather than enumerating every kind.

ALTER TABLE media_items ADD COLUMN kind TEXT;

-- Partial index — most rows are null, no point indexing them.
-- Used for "show me all the OVAs / specials" library filters and
-- for the UI's "regular episodes only" filter that excludes
-- kind IS NOT NULL.
CREATE INDEX idx_media_items_kind ON media_items(kind) WHERE kind IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_media_items_kind;
ALTER TABLE media_items DROP COLUMN IF EXISTS kind;
