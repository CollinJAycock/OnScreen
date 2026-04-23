-- +goose Up
-- DVR recordings land in a dedicated library so users can retention-
-- manage them independently from their main movie/show collections.
-- The 'dvr' type reuses the existing media_items surface — recordings
-- finalize as `type='movie'` or `type='episode'` rows just like any
-- other scanned media.

ALTER TABLE libraries DROP CONSTRAINT libraries_type_check;
ALTER TABLE libraries ADD CONSTRAINT libraries_type_check
    CHECK (type IN ('movie', 'show', 'music', 'photo', 'dvr'));

-- +goose Down
-- Ensure no orphans before narrowing the constraint. DVR libraries
-- would survive with media_items intact if the operator rolls back,
-- but the CHECK rejects them — delete the library rows in the down
-- path so goose-down-to-0 succeeds in round-trip tests.
DELETE FROM libraries WHERE type = 'dvr';
ALTER TABLE libraries DROP CONSTRAINT libraries_type_check;
ALTER TABLE libraries ADD CONSTRAINT libraries_type_check
    CHECK (type IN ('movie', 'show', 'music', 'photo'));
