-- +goose Up
-- AniList + MyAnimeList ID columns on media_items so anime rows can
-- carry cross-references to the anime-native metadata providers.
--
-- AniList is the primary anime metadata source for v2.2 (GraphQL,
-- 90 req/min unauthenticated); MAL IDs are the more universal
-- cross-reference and AniList returns them on every Media row, so we
-- store both alongside the existing tmdb_id / tvdb_id / imdb_id.
--
-- Both nullable: anime rows get them populated by the AniList agent;
-- non-anime rows keep them NULL. Partial indexes mirror the existing
-- TMDB index pattern so refresh-by-id lookups stay fast without
-- bloating the index for the common case of NULLs.

ALTER TABLE media_items ADD COLUMN anilist_id INT;
ALTER TABLE media_items ADD COLUMN mal_id INT;

CREATE INDEX idx_media_items_anilist ON media_items(anilist_id) WHERE anilist_id IS NOT NULL;
CREATE INDEX idx_media_items_mal     ON media_items(mal_id)     WHERE mal_id     IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_media_items_mal;
DROP INDEX IF EXISTS idx_media_items_anilist;

ALTER TABLE media_items DROP COLUMN IF EXISTS mal_id;
ALTER TABLE media_items DROP COLUMN IF EXISTS anilist_id;
