-- +goose Up
-- +goose StatementBegin
-- Per-user hub row customization: ordered list of {key, enabled} entries.
-- Keys are row identifiers the clients understand ("continue_tv",
-- "continue_movies", "continue_other", "trending", "library:<uuid>").
-- NULL = the default layout (everything visible, default order). Stored as
-- JSONB rather than columns because the per-library entries vary by user
-- and library set.
ALTER TABLE users ADD COLUMN hub_layout jsonb;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE users DROP COLUMN IF EXISTS hub_layout;
-- +goose StatementEnd
