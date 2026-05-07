-- +goose Up
-- franchise_id groups anime cours that share an AniList franchise
-- relations graph (PREQUEL/SEQUEL/PARENT edges, filtered to the
-- TV / TV_SHORT formats so OVAs and movies don't pull in the chain).
--
-- The value is the *smallest* AniList ID in the connected component —
-- a stable, deterministic key without needing a separate franchises
-- table. Two cours that share a franchise_id can be grouped in the UI
-- without an extra join. NULL = not part of any walked franchise yet
-- (most non-anime rows, plus anime rows scanned before this column
-- existed; the enricher backfills as it touches each row).
--
-- The community-converged solution per Plex/Hama (anime-list XML),
-- Jellyfin/Shoko (its relation engine), and AniList itself: trust
-- the relations graph instead of regexing titles like "Dr. STONE:
-- STONE WARS" / "Free! -Dive to the Future-" / "Sailor Moon SuperS"
-- which collide with legitimate non-anime titles. The graph walk is
-- already implemented (anilist.GetAnimeFranchise); this column gives
-- the result a place to land.

ALTER TABLE media_items ADD COLUMN franchise_id INT;

CREATE INDEX idx_media_items_franchise
    ON media_items(franchise_id)
    WHERE franchise_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_media_items_franchise;

ALTER TABLE media_items DROP COLUMN IF EXISTS franchise_id;
