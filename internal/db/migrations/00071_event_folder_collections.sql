-- +goose Up
-- Event-folder collections: when a home_video library is laid out as
-- `<root>/<EventName>/<files>` (e.g. `Home Videos/Yellowstone 2024/`),
-- the scanner auto-creates a collection per event folder and adds
-- every file under that folder to it. This is the home-video analog
-- of "movie in a series gets added to the series collection" — the
-- folder *is* the trip / event grouping, and surfacing it as a
-- collection beats forcing the user to hand-build one for every
-- weekend.
--
-- Schema additions:
--
--   1. library_id column on collections so an event collection is
--      scoped to its library. Without scoping, two home_video
--      libraries that both have a "Birthdays" folder would collide on
--      the unique index. NULL for non-event collection types
--      (auto_genre / playlist / smart_playlist) — they're all
--      library-agnostic today.
--
--   2. 'event_folder' type added to the CHECK so the collection can
--      be distinguished from user-created playlists in the UI (we
--      render them as a separate "Events" shelf on the home_video
--      library page rather than mixed into the user playlists view).
--
--   3. Partial unique index on (library_id, name) WHERE
--      type='event_folder'. Lets the scanner upsert via ON CONFLICT
--      cleanly — without this, concurrent goroutines processing two
--      files in the same event folder would race-create two
--      collections with the same name.
ALTER TABLE collections ADD COLUMN library_id UUID
    REFERENCES libraries(id) ON DELETE CASCADE;

ALTER TABLE collections DROP CONSTRAINT collections_type_check;
ALTER TABLE collections ADD CONSTRAINT collections_type_check
    CHECK (type IN ('auto_genre', 'playlist', 'smart_playlist', 'event_folder'));

CREATE UNIQUE INDEX collections_event_folder_unique
    ON collections (library_id, name)
    WHERE type = 'event_folder';

CREATE INDEX collections_library_id ON collections (library_id)
    WHERE library_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS collections_library_id;
DROP INDEX IF EXISTS collections_event_folder_unique;

DELETE FROM collections WHERE type = 'event_folder';

ALTER TABLE collections DROP CONSTRAINT collections_type_check;
ALTER TABLE collections ADD CONSTRAINT collections_type_check
    CHECK (type IN ('auto_genre', 'playlist', 'smart_playlist'));

ALTER TABLE collections DROP COLUMN IF EXISTS library_id;
