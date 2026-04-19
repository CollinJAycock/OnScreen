-- +goose Up
CREATE TABLE user_favorites (
    user_id     UUID NOT NULL REFERENCES users(id)        ON DELETE CASCADE,
    media_id    UUID NOT NULL REFERENCES media_items(id)  ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, media_id)
);
CREATE INDEX idx_user_favorites_user_created ON user_favorites(user_id, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS user_favorites;
