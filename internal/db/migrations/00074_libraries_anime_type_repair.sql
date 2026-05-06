-- +goose Up
-- Repair migration for the v2.2 anime track. An earlier draft of
-- 00073 added an `is_anime BOOLEAN` column on `libraries`. The
-- design then pivoted to anime as a first-class library type — so
-- 00073 was rewritten in place to update the type-check constraint
-- instead of adding a column. Dev DBs that ran the *original* 00073
-- have the column but not the constraint update; goose tracks
-- version numbers (not file content) so the rewritten 00073 never
-- re-executes. This 00074 reconciles by dropping the orphan column
-- and re-asserting the constraint.
--
-- Idempotent on every path:
--   - Fresh installs (post-rewrite 00073): no `is_anime` column to
--     drop; the constraint is already correct so the DROP/ADD is a
--     no-op churn.
--   - Existing dev DBs (pre-rewrite 00073): the `is_anime` column
--     gets dropped and the constraint flips to include 'anime'.

ALTER TABLE libraries DROP COLUMN IF EXISTS is_anime;

ALTER TABLE libraries DROP CONSTRAINT libraries_type_check;
ALTER TABLE libraries ADD CONSTRAINT libraries_type_check
    CHECK (type IN ('movie', 'show', 'music', 'photo', 'dvr', 'audiobook', 'podcast', 'home_video', 'book', 'anime'));

-- +goose Down
-- Reparent any anime libraries to 'show' before tightening the
-- constraint, so the down migration doesn't violate the new check.
UPDATE libraries SET type = 'show' WHERE type = 'anime';

ALTER TABLE libraries DROP CONSTRAINT libraries_type_check;
ALTER TABLE libraries ADD CONSTRAINT libraries_type_check
    CHECK (type IN ('movie', 'show', 'music', 'photo', 'dvr', 'audiobook', 'podcast', 'home_video', 'book'));
