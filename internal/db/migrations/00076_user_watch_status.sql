-- +goose Up
-- Per-user watching-status mirror. Anime-tracker convention
-- (MyAnimeList / AniList / Kitsu shape) but we ship it as a generic
-- per-(user, item) feature so every type benefits — TV shows,
-- movies, audiobooks, even music albums can carry "Plan to Listen /
-- Listening / Completed" without a parallel schema.
--
-- Enum kept as TEXT + CHECK rather than a Postgres ENUM so a future
-- status (e.g. "rewatching") can land via a column-comment update +
-- new CHECK without an ALTER TYPE migration. CHECK still gives us
-- a server-side guard against stray values from buggy clients.
--
-- One row per (user, item). PK collapses upserts to a single row;
-- the secondary (user_id, status) index drives "show me all my
-- Plan to Watch shows" list views without scanning the table.
CREATE TABLE user_watch_status (
    user_id       UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    media_item_id UUID         NOT NULL REFERENCES media_items(id) ON DELETE CASCADE,
    status        TEXT         NOT NULL CHECK (status IN (
        'plan_to_watch',
        'watching',
        'completed',
        'on_hold',
        'dropped'
    )),
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, media_item_id)
);

CREATE INDEX idx_user_watch_status_user_status
    ON user_watch_status(user_id, status);

-- +goose Down
DROP INDEX IF EXISTS idx_user_watch_status_user_status;
DROP TABLE IF EXISTS user_watch_status;
