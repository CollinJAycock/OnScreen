-- +goose Up
-- Flat music libraries ("/Music/Artist/track.flac" with every album's
-- tracks directly in the artist folder) hit a scanner bug where each
-- album's embedded art was written to the same `<dir>/poster.jpg` and
-- the DB pointed every album at that one file. The fix renames the
-- on-disk file to `<album.id>-poster.jpg` (per-album, collision-free),
-- but existing rows still reference the shared legacy path.
--
-- Null out only the poster_path values that are actually shared across
-- multiple albums — those are the demonstrably broken ones. Albums with
-- their own unique poster_path (proper per-album subfolders) keep what
-- they have and don't need a rescan. The next scan of a cleared album
-- re-extracts embedded art into the new ID-qualified filename and
-- updates poster_path accordingly.

UPDATE media_items
SET poster_path = NULL,
    updated_at  = NOW()
WHERE type = 'album'
  AND poster_path IS NOT NULL
  AND poster_path IN (
      SELECT poster_path
      FROM media_items
      WHERE type = 'album' AND poster_path IS NOT NULL AND deleted_at IS NULL
      GROUP BY poster_path
      HAVING COUNT(*) > 1
  );

-- +goose Down
-- Irreversible: the original per-album poster_path values are gone.
-- A no-op Down keeps the round-trip test happy; the next scan refills
-- whatever it can.
SELECT 1;
