-- +goose Up
ALTER TABLE media_items DROP CONSTRAINT media_items_type_check;
ALTER TABLE media_items ADD CONSTRAINT media_items_type_check
  CHECK (type IN ('movie','show','season','episode','track','album','artist','photo'));

-- +goose Down
DELETE FROM media_files WHERE media_item_id IN (SELECT id FROM media_items WHERE type = 'photo');
DELETE FROM media_items WHERE type = 'photo';
ALTER TABLE media_items DROP CONSTRAINT media_items_type_check;
ALTER TABLE media_items ADD CONSTRAINT media_items_type_check
  CHECK (type IN ('movie','show','season','episode','track','album','artist'));
