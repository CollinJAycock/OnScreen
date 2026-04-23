-- +goose Up
-- Some music items ended up with poster_path / fanart_path values that
-- are just a bare filename (e.g. "9d7c47c6-…-poster.jpg") because the
-- enricher's relPath fallback returned only the basename whenever its
-- scanPaths() lookup missed or errored. The /artwork/* handler joins
-- each library scan root with the stored relpath, so a bare filename
-- only resolves when the file happens to sit at the scan root — it
-- almost never does for music (artwork lives one level deep inside the
-- artist folder), and every such row 404s.
--
-- Null those out so the next scan refills them with the correct
-- "<artist>/<uuid>-poster.jpg" form via the scanner's now-consistent
-- fallback. Rows with a slash in the path are presumed valid and left
-- alone; rows without a slash will be re-populated from the embedded
-- album art on disk (extractAlbumArt detects the existing file and
-- writes a fresh DB path on each scan pass).
--
-- Scoped to type IN ('album','artist') to avoid touching movies / TV /
-- other libraries where bare filenames are legitimate (poster.jpg at
-- the library root — e.g. an Open Directory-style music library with
-- one album total).

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

-- +goose Down
-- Irreversible; the next scan will refill. No-op keeps round-trip
-- migration tests happy.
SELECT 1;
