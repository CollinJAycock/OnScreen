-- +goose Up
-- Migration 00054 nulled bare-filename poster_path values for albums
-- but didn't fix the writer — the enricher's relPath fallback at the
-- time returned filepath.Base(absPath) on any path it couldn't resolve
-- against scanPaths(), which could happen on:
--
--   - cover-art-archive enrichment when scanPaths() returned stale
--     after a library remount, or
--   - libraries on filesystems where filepath.Rel disagreed with the
--     stored scan_path (symlinks, bind mounts, container path
--     remapping).
--
-- Subsequent enricher passes silently re-broke the rows 00054 had
-- nulled, producing /artwork/<albumId>-poster.jpg URLs that 404
-- because the file actually lives at /artwork/<artist>/<album>/...
--
-- The enricher's relPath now returns "" on failure and callers skip
-- the update, so future runs won't re-introduce the regression.
-- This migration cleans up the rows already in QA + prod from the
-- regression window.
--
-- Scope: type IN ('album', 'artist'). Music posters are the
-- predominantly-affected ones because the music enricher's CAA + AudioDB
-- branches both go through relPath. Movies / shows / photos with bare
-- filenames could legitimately exist (cover.jpg at a single-file
-- library root) and aren't touched.

UPDATE media_items
SET poster_path = NULL,
    updated_at  = NOW()
WHERE type IN ('album', 'artist')
  AND poster_path IS NOT NULL
  AND poster_path NOT LIKE '%/%';

UPDATE media_items
SET fanart_path = NULL,
    updated_at  = NOW()
WHERE type IN ('album', 'artist')
  AND fanart_path IS NOT NULL
  AND fanart_path NOT LIKE '%/%';

UPDATE media_items
SET thumb_path = NULL,
    updated_at  = NOW()
WHERE type IN ('album', 'artist')
  AND thumb_path IS NOT NULL
  AND thumb_path NOT LIKE '%/%';

-- +goose Down
-- Irreversible; the next scan refills via the scanner's own primary
-- match (which uses the library's scan_paths directly and resolves
-- cleanly to <Artist>/<uuid>-poster.jpg).
