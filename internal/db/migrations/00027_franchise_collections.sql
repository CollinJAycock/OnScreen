-- +goose Up
-- +goose StatementBegin
-- Franchise collections: TMDB "belongs_to_collection" groupings (the Alien
-- films, the Toy Story films, …) surfaced as server-owned collections once the
-- server holds at least two movies of the same TMDB collection.
--
-- Three pieces:
--   1. collections gains the 'franchise' type and a tmdb_collection_id that is
--      unique among the rows that carry one, so the enrichment-time upsert is a
--      single ON CONFLICT and two concurrent enrichments of sibling movies
--      converge on one row. Membership reuses collection_items (positions in
--      release order), so every existing collection read path — items listing,
--      ACL, rating ceiling — applies unchanged.
--   2. tmdb_collections is the stored snapshot of TMDB's /collection/{id}
--      response (name, art, every part). The collection page and the movie
--      page's "Part of…" shelf read it instead of calling TMDB per page view,
--      and it keeps working while TMDB is unreachable.
--   3. media_item_tmdb_collections records which TMDB collection each movie
--      belongs to, as of the TMDB id it was checked for. A row with a NULL
--      tmdb_collection_id means "checked, belongs to none", so the backfill
--      task never re-asks TMDB about a standalone film; a row whose tmdb_id no
--      longer matches the movie's (Fix Match) is stale and re-checked. Kept in
--      its own table rather than as a media_items column so the hot
--      media_items row shape (and every SELECT * model built on it) is left
--      alone.
ALTER TABLE public.collections DROP CONSTRAINT IF EXISTS collections_type_check;
ALTER TABLE public.collections ADD CONSTRAINT collections_type_check
  CHECK (type = ANY (ARRAY['auto_genre', 'playlist', 'smart_playlist', 'event_folder', 'photo_album', 'franchise']::text[]));

ALTER TABLE public.collections ADD COLUMN tmdb_collection_id integer;

CREATE UNIQUE INDEX collections_tmdb_collection_id_key
    ON public.collections (tmdb_collection_id)
    WHERE tmdb_collection_id IS NOT NULL;

CREATE TABLE public.tmdb_collections (
    tmdb_collection_id integer PRIMARY KEY,
    name text NOT NULL,
    overview text,
    poster_url text,
    backdrop_url text,
    -- [{tmdb_id, title, release_date, poster_url, overview}], release order.
    parts jsonb DEFAULT '[]'::jsonb NOT NULL,
    fetched_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE public.media_item_tmdb_collections (
    media_item_id uuid PRIMARY KEY
        REFERENCES public.media_items(id) ON DELETE CASCADE,
    -- The movie's TMDB id at check time; a mismatch with media_items.tmdb_id
    -- marks the row stale (the movie was re-matched).
    tmdb_id integer NOT NULL,
    -- NULL = checked, the movie belongs to no TMDB collection.
    tmdb_collection_id integer,
    checked_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE INDEX media_item_tmdb_collections_collection
    ON public.media_item_tmdb_collections (tmdb_collection_id)
    WHERE tmdb_collection_id IS NOT NULL;

-- Every movie detail now asks "which franchise is this in?"
-- (collection_items by media_item_id); only collection_id was indexed. Also
-- speeds the ON DELETE CASCADE from media_items.
CREATE INDEX IF NOT EXISTS idx_collection_items_media_item
    ON public.collection_items (media_item_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS public.idx_collection_items_media_item;
DROP TABLE IF EXISTS public.media_item_tmdb_collections;
DROP TABLE IF EXISTS public.tmdb_collections;
-- Franchise rows are derived data (rebuilt from TMDB on the next enrichment
-- pass after a re-upgrade); the restored CHECK would reject them, so they go.
-- collection_items rows cascade with them.
DELETE FROM public.collections WHERE type = 'franchise';
DROP INDEX IF EXISTS public.collections_tmdb_collection_id_key;
ALTER TABLE public.collections DROP COLUMN IF EXISTS tmdb_collection_id;
ALTER TABLE public.collections DROP CONSTRAINT IF EXISTS collections_type_check;
ALTER TABLE public.collections ADD CONSTRAINT collections_type_check
  CHECK (type = ANY (ARRAY['auto_genre', 'playlist', 'smart_playlist', 'event_folder', 'photo_album']::text[]));
-- +goose StatementEnd
