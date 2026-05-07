-- +goose Up
-- media_files.file_path was globally UNIQUE since 00001_init. That
-- broke the "delete a library, recreate at the same path" flow:
-- library soft-delete leaves media_items / media_files orphaned
-- behind the deleted_at row, so the new library's scan walks the
-- same paths, hits the existing rows via GetMediaFileByPath, and
-- treats every file as "already scanned, not new" — the symptom
-- was a freshly-created library reporting found:5870 / new:0 with
-- nothing visible to the user.
--
-- The fix is twofold:
--   1. (this migration) Replace the global UNIQUE with a partial
--      unique excluding deleted-status rows, so a re-scan after
--      cascade-soft-delete can claim the path.
--   2. (companion service change) Library.Delete() now also marks
--      the library's media_files status='deleted', so the partial
--      unique recognises them as inactive and lets the new library
--      insert fresh rows at the same paths.
--
-- The maintenance endpoint /maintenance/purge-deleted-library
-- hard-removes orphan rows from libraries that were soft-deleted
-- before this migration landed (one-time cleanup for QA).

ALTER TABLE media_files DROP CONSTRAINT media_files_file_path_key;

CREATE UNIQUE INDEX media_files_file_path_active_key
    ON media_files(file_path)
    WHERE status != 'deleted';

-- +goose Down
DROP INDEX IF EXISTS media_files_file_path_active_key;

-- Re-add the global UNIQUE. This will fail if rows with status =
-- 'deleted' share a file_path with active rows — operator should
-- run /maintenance/purge-deleted-library before rolling back, or
-- manually clean up colliding rows.
ALTER TABLE media_files ADD CONSTRAINT media_files_file_path_key UNIQUE (file_path);
