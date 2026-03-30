-- +goose Up
CREATE INDEX IF NOT EXISTS idx_media_items_year ON media_items(year) WHERE deleted_at IS NULL AND year IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_media_items_rating ON media_items(rating DESC NULLS LAST) WHERE deleted_at IS NULL AND rating IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_media_items_created ON media_items(created_at DESC) WHERE deleted_at IS NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_media_items_year;
DROP INDEX IF EXISTS idx_media_items_rating;
DROP INDEX IF EXISTS idx_media_items_created;
