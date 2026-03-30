-- +goose Up
-- +goose StatementBegin

-- Issue #6: watch_events.user_id → ON DELETE CASCADE
ALTER TABLE watch_events DROP CONSTRAINT IF EXISTS watch_events_user_id_fkey;
ALTER TABLE watch_events ADD CONSTRAINT watch_events_user_id_fkey
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;

-- Issue #26: watch_events.media_id → ON DELETE CASCADE
ALTER TABLE watch_events DROP CONSTRAINT IF EXISTS watch_events_media_id_fkey;
ALTER TABLE watch_events ADD CONSTRAINT watch_events_media_id_fkey
  FOREIGN KEY (media_id) REFERENCES media_items(id) ON DELETE CASCADE;

-- Issue #27: watch_events.file_id → ON DELETE SET NULL
ALTER TABLE watch_events DROP CONSTRAINT IF EXISTS watch_events_file_id_fkey;
ALTER TABLE watch_events ADD CONSTRAINT watch_events_file_id_fkey
  FOREIGN KEY (file_id) REFERENCES media_files(id) ON DELETE SET NULL;

-- Issue #28: media_files.media_item_id → ON DELETE CASCADE
ALTER TABLE media_files DROP CONSTRAINT IF EXISTS media_files_media_item_id_fkey;
ALTER TABLE media_files ADD CONSTRAINT media_files_media_item_id_fkey
  FOREIGN KEY (media_item_id) REFERENCES media_items(id) ON DELETE CASCADE;

-- Issue #28: media_items.parent_id → ON DELETE CASCADE
ALTER TABLE media_items DROP CONSTRAINT IF EXISTS media_items_parent_id_fkey;
ALTER TABLE media_items ADD CONSTRAINT media_items_parent_id_fkey
  FOREIGN KEY (parent_id) REFERENCES media_items(id) ON DELETE CASCADE;

-- media_items.library_id → ON DELETE CASCADE
ALTER TABLE media_items DROP CONSTRAINT IF EXISTS media_items_library_id_fkey;
ALTER TABLE media_items ADD CONSTRAINT media_items_library_id_fkey
  FOREIGN KEY (library_id) REFERENCES libraries(id) ON DELETE CASCADE;

-- Issue #32: default partition for watch_events
CREATE TABLE IF NOT EXISTS watch_events_default PARTITION OF watch_events DEFAULT;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS watch_events_default;

-- Restore original FKs without CASCADE
ALTER TABLE media_items DROP CONSTRAINT IF EXISTS media_items_library_id_fkey;
ALTER TABLE media_items ADD CONSTRAINT media_items_library_id_fkey
  FOREIGN KEY (library_id) REFERENCES libraries(id);

ALTER TABLE media_items DROP CONSTRAINT IF EXISTS media_items_parent_id_fkey;
ALTER TABLE media_items ADD CONSTRAINT media_items_parent_id_fkey
  FOREIGN KEY (parent_id) REFERENCES media_items(id);

ALTER TABLE media_files DROP CONSTRAINT IF EXISTS media_files_media_item_id_fkey;
ALTER TABLE media_files ADD CONSTRAINT media_files_media_item_id_fkey
  FOREIGN KEY (media_item_id) REFERENCES media_items(id);

ALTER TABLE watch_events DROP CONSTRAINT IF EXISTS watch_events_file_id_fkey;
ALTER TABLE watch_events ADD CONSTRAINT watch_events_file_id_fkey
  FOREIGN KEY (file_id) REFERENCES media_files(id);

ALTER TABLE watch_events DROP CONSTRAINT IF EXISTS watch_events_media_id_fkey;
ALTER TABLE watch_events ADD CONSTRAINT watch_events_media_id_fkey
  FOREIGN KEY (media_id) REFERENCES media_items(id);

ALTER TABLE watch_events DROP CONSTRAINT IF EXISTS watch_events_user_id_fkey;
ALTER TABLE watch_events ADD CONSTRAINT watch_events_user_id_fkey
  FOREIGN KEY (user_id) REFERENCES users(id);

-- +goose StatementEnd
