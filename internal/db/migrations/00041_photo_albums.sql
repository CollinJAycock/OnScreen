-- +goose Up
-- Photo albums are user-curated groupings of photo media items. We reuse the
-- existing collections + collection_items tables (same pattern as playlists)
-- so we get position, ON DELETE CASCADE, and the (collection_id, media_item_id)
-- uniqueness constraint for free. The only schema change is widening the
-- type CHECK to allow the new 'photo_album' value.
ALTER TABLE collections DROP CONSTRAINT IF EXISTS collections_type_check;
ALTER TABLE collections ADD CONSTRAINT collections_type_check
    CHECK (type IN ('auto_genre', 'playlist', 'photo_album'));

-- +goose Down
ALTER TABLE collections DROP CONSTRAINT IF EXISTS collections_type_check;
ALTER TABLE collections ADD CONSTRAINT collections_type_check
    CHECK (type IN ('auto_genre', 'playlist'));
