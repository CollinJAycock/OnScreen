-- +goose Up
-- Album items ended up with poster_path / fanart_path values that are
-- just a bare filename (e.g. "9d7c47c6-…-poster.jpg") because the
-- enricher's relPath fallback returned only the basename whenever its
-- scanPaths() lookup missed. Album art lives one level deep (inside
-- <Artist>/), so a bare filename 404s against <scan_root>/<filename>
-- — there's no album art file there.
--
-- Null those out so the next scan refills via the scanner's own
-- primary match, which uses the library's scan_paths directly and
-- resolves cleanly to "<Artist>/<uuid>-poster.jpg".
--
-- Scoped to type='album' ONLY. Artist artwork on flat-layout music
-- libraries (tracks directly inside <Artist>/ with no <Album>/ below)
-- legitimately sits AT the scan root, so a bare filename is the
-- correct value there and nulling it would send those rows through
-- the enricher's singleflight-gated re-fetch path unnecessarily.
--
-- Movies and TV libraries are excluded for the same reason — a
-- Plex-style single-album music library or a single-movie layout
-- may put poster.jpg at the library root, and bare filenames are
-- legitimate for those.

UPDATE media_items
SET poster_path = NULL,
    updated_at  = NOW()
WHERE type = 'album'
  AND poster_path IS NOT NULL
  AND poster_path NOT LIKE '%/%';

UPDATE media_items
SET fanart_path = NULL,
    updated_at  = NOW()
WHERE type = 'album'
  AND fanart_path IS NOT NULL
  AND fanart_path NOT LIKE '%/%';

-- +goose Down
-- Irreversible; the next scan will refill. No-op keeps round-trip
-- migration tests happy.
SELECT 1;
