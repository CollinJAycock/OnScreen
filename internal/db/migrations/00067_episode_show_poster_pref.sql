-- +goose Up
-- Per-user toggle for "use the series poster on episodes" — episode
-- thumbnails are often a noisy frame-grab from the episode itself,
-- which makes Continue Watching / History / Search rows look messy
-- compared to the show's curated poster. Default ON because that's
-- the behaviour the feature was requested for; users who want
-- distinct episode thumbs can flip it off in settings.
--
-- Only affects browse surfaces (hub, history, search, item detail
-- for an individual episode). The season → episodes child list
-- still shows distinct episode art so episodes stay
-- distinguishable inside a season.
ALTER TABLE users
    ADD COLUMN episode_use_show_poster BOOLEAN NOT NULL DEFAULT TRUE;

-- +goose Down
ALTER TABLE users DROP COLUMN IF EXISTS episode_use_show_poster;
