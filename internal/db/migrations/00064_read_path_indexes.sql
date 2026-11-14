-- +goose Up
-- +goose StatementBegin
-- Read-path indexes for the queries a big library pays for on every page,
-- plus two pieces of dead weight removed. Plain CREATE INDEX inside goose's
-- transaction, like every earlier media_items index (00002, 00047, 00062):
-- the builds take seconds at self-hosted scale and hold off writes only
-- for that long.
--
-- 1. The library grid. ListMediaItems / ListMediaItemsBy* filter on
--    (library_id, type, deleted_at IS NULL) and order by one of four keys;
--    CountMediaItems* and ListMediaItemLetters share the filter. The only
--    usable index was idx_media_items_library (library_id), so listing 50
--    shows read every season and episode of the TV library to sort 1,500
--    rows, and 50 artists read every album and track. One index per sort
--    the grid offers by default (title, date added, year and rating, the
--    last three descending). Title and date added, the default listing and
--    the home screen's "Recently added" link, carry the ORDER BY's
--    tie-breakers so a page at any OFFSET is a straight index range. Year
--    and rating stop at the sort key: an incremental sort orders the few
--    rows that share the first page's year or rating, and leaving
--    sort_title out lets B-tree deduplication shrink them to a fraction of
--    the size (3.5 MB and 23 MB instead of 58 MB each on a 500k-item test
--    library). The reverse directions read these backwards (title, date
--    added) or sort just the library + type range, still far less than
--    before.
CREATE INDEX idx_media_items_lib_type_title ON public.media_items
    USING btree (library_id, type, sort_title, id)
    WHERE deleted_at IS NULL;
CREATE INDEX idx_media_items_lib_type_added ON public.media_items
    USING btree (library_id, type, created_at DESC, id)
    WHERE deleted_at IS NULL;
CREATE INDEX idx_media_items_lib_type_year ON public.media_items
    USING btree (library_id, type, year DESC NULLS LAST)
    WHERE deleted_at IS NULL;
CREATE INDEX idx_media_items_lib_type_rating ON public.media_items
    USING btree (library_id, type, rating DESC NULLS LAST)
    WHERE deleted_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
-- 2. The artwork gate. For a rating-capped or label-restricted profile every
--    /artwork/* request runs GetArtworkContentRating and
--    ArtworkLabelsPermitted: `lower(<col>) = lower($path)` OR'd over the six
--    art columns, scoped to one library. Nothing indexed any of them, so a
--    60-poster grid was 60 scans of the library. One expression index per
--    column lets the planner answer the OR with a BitmapOr of six probes.
--    Partial on `<col> IS NOT NULL` keeps the mostly-empty logo / banner /
--    disc-art indexes near zero size; the equality implies it, so the
--    planner still matches them.
--
--    The CREATE STATISTICS objects matter as much as the indexes: Postgres
--    ignores a PARTIAL expression index when estimating, so without them
--    `lower(col) = $1` fell back to the default 0.5% selectivity, the
--    planner expected thousands of matches, and GetArtworkContentRating's
--    LIMIT 1 made a library scan look cheaper than the BitmapOr. With
--    per-expression statistics it picks the BitmapOr in custom and generic
--    plans alike.
CREATE INDEX idx_media_items_art_poster ON public.media_items
    USING btree (library_id, lower(poster_path))
    WHERE deleted_at IS NULL AND poster_path IS NOT NULL;
CREATE INDEX idx_media_items_art_fanart ON public.media_items
    USING btree (library_id, lower(fanart_path))
    WHERE deleted_at IS NULL AND fanart_path IS NOT NULL;
CREATE INDEX idx_media_items_art_thumb ON public.media_items
    USING btree (library_id, lower(thumb_path))
    WHERE deleted_at IS NULL AND thumb_path IS NOT NULL;
CREATE INDEX idx_media_items_art_logo ON public.media_items
    USING btree (library_id, lower(logo_path))
    WHERE deleted_at IS NULL AND logo_path IS NOT NULL;
CREATE INDEX idx_media_items_art_banner ON public.media_items
    USING btree (library_id, lower(banner_path))
    WHERE deleted_at IS NULL AND banner_path IS NOT NULL;
CREATE INDEX idx_media_items_art_disc ON public.media_items
    USING btree (library_id, lower(disc_art_path))
    WHERE deleted_at IS NULL AND disc_art_path IS NOT NULL;
CREATE STATISTICS public.media_items_art_poster_stats ON (lower(poster_path)) FROM public.media_items;
CREATE STATISTICS public.media_items_art_fanart_stats ON (lower(fanart_path)) FROM public.media_items;
CREATE STATISTICS public.media_items_art_thumb_stats ON (lower(thumb_path)) FROM public.media_items;
CREATE STATISTICS public.media_items_art_logo_stats ON (lower(logo_path)) FROM public.media_items;
CREATE STATISTICS public.media_items_art_banner_stats ON (lower(banner_path)) FROM public.media_items;
CREATE STATISTICS public.media_items_art_disc_stats ON (lower(disc_art_path)) FROM public.media_items;
-- +goose StatementEnd

-- +goose StatementBegin
-- 3. The /jobs counters (CountMediaItemsMissingArt, CountUnmatchedTopLevelItems),
--    polled every 30 s by each open admin tab, were sequential scans of
--    media_items. Partial indexes with exactly their predicates make each an
--    index-only count over the handful of rows involved; the key columns
--    match the ORDER BY of the list queries that share the predicates
--    (ListMediaItemsMissingArt, ListUnmatchedTopLevelItems).
CREATE INDEX idx_media_items_missing_art ON public.media_items
    USING btree (created_at DESC)
    WHERE type IN ('movie', 'show') AND poster_path IS NULL AND deleted_at IS NULL;
CREATE INDEX idx_media_items_unmatched ON public.media_items
    USING btree (created_at)
    WHERE parent_id IS NULL AND deleted_at IS NULL AND type IN ('movie', 'show')
      AND tmdb_id IS NULL AND tvdb_id IS NULL AND imdb_id IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
-- 4. Genre pages (ListItemsByGenre / CountItemsByGenre) now test
--    `genres @> ARRAY[$1]`, which a GIN index can answer; `$1 = ANY(genres)`
--    could only be checked row by row. Partial on the same predicate the
--    queries carry.
CREATE INDEX idx_media_items_genres ON public.media_items
    USING gin (genres)
    WHERE deleted_at IS NULL AND type IN ('movie', 'show');
-- +goose StatementEnd

-- +goose StatementBegin
-- 5. The photo timeline sorted on COALESCE(photo_metadata.taken_at,
--    media_items.created_at) across a LEFT JOIN, which no index can serve:
--    every page (and its COUNT) joined and sorted the whole photo library.
--    originally_available_at, which the scanner mirrors the EXIF date onto,
--    is a DATE, so sorting on it would scramble photos taken the same day.
--    Instead media_items carries a copy of photo_metadata.taken_at, kept by
--    a trigger on photo_metadata so every writer (the scanner, a future
--    editor, a cascade) keeps it in step, and the timeline index covers the
--    exact sort key. Not named taken_at: queries that join photo_metadata
--    use that name unqualified.
ALTER TABLE public.media_items ADD COLUMN photo_taken_at timestamp with time zone;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.photo_metadata_sync_taken_at() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    -- A delete (direct, or cascaded from the photo's own item, which then
    -- matches no live row) or a row moved to another item clears the old
    -- item's copy: the timeline falls back to created_at, as the LEFT JOIN did.
    IF TG_OP = 'DELETE' OR (TG_OP = 'UPDATE' AND OLD.item_id IS DISTINCT FROM NEW.item_id) THEN
        UPDATE public.media_items SET photo_taken_at = NULL
        WHERE id = OLD.item_id AND photo_taken_at IS NOT NULL;
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN NULL;
    END IF;
    -- IS DISTINCT FROM: a rescan rewrites the same date; skip the dead tuple
    -- a no-op update would leave.
    UPDATE public.media_items SET photo_taken_at = NEW.taken_at
    WHERE id = NEW.item_id AND photo_taken_at IS DISTINCT FROM NEW.taken_at;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- Created before the backfill: CREATE TRIGGER locks photo_metadata against
-- writes for the rest of this transaction, so no EXIF row can land between
-- the backfill and the trigger going live (same order as 00023).
CREATE TRIGGER photo_metadata_sync_taken_at
    AFTER INSERT OR DELETE OR UPDATE OF taken_at, item_id ON public.photo_metadata
    FOR EACH ROW EXECUTE FUNCTION public.photo_metadata_sync_taken_at();
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE public.media_items mi
SET photo_taken_at = pm.taken_at
FROM public.photo_metadata pm
WHERE pm.item_id = mi.id AND pm.taken_at IS NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
-- Matches ListPhotosByLibrary's ORDER BY exactly (both keys descending), so
-- a page is an index range read plus one photo_metadata probe per row.
CREATE INDEX idx_media_items_photo_timeline ON public.media_items
    USING btree (library_id, (COALESCE(photo_taken_at, created_at)) DESC, id DESC)
    WHERE type = 'photo' AND deleted_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
-- 6. content_rating_rank() was PL/pgSQL, so every rating-capped query paid an
--    opaque function call per row and could not run in parallel. As one SQL
--    CASE expression, IMMUTABLE and PARALLEL SAFE, the planner inlines it
--    (00061 did the same for the label check). Same ladder as 00016: a NULL
--    or '' rating matches no WHEN and lands in ELSE 4, exactly where the old
--    IF sent it. CREATE OR REPLACE keeps the signature, so callers and
--    media_watch_bucket are untouched.
CREATE OR REPLACE FUNCTION public.content_rating_rank(rating text) RETURNS integer
    LANGUAGE sql IMMUTABLE PARALLEL SAFE
    AS $$
    SELECT CASE rating
        WHEN 'G'     THEN 0  WHEN 'TV-Y'  THEN 0  WHEN 'TV-G' THEN 0
        WHEN 'PG'    THEN 1  WHEN 'TV-Y7' THEN 1  WHEN 'TV-PG' THEN 1
        WHEN 'PG-13' THEN 2  WHEN 'TV-14' THEN 2  WHEN 'R-15' THEN 2
        WHEN 'R'     THEN 3  WHEN 'R-17+' THEN 3
        WHEN 'NC-17' THEN 3  WHEN 'TV-MA' THEN 3  WHEN 'R+'   THEN 3  WHEN 'R-18+' THEN 3
        -- Rx (hentai): most-restrictive bucket so a TV-MA/NC-17 ceiling still blocks it.
        WHEN 'Rx'    THEN 4
        ELSE 4  -- NULL, '', NR, UNRATED, X, etc.
    END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- 7. hub_recently_added was refreshed every five minutes and after every
--    watcher scan, but nothing read it: the home hub's Recently Added rows
--    query media_items directly (ListRecentlyAdded*). The refresh worker and
--    its query are gone; so is the view.
DROP MATERIALIZED VIEW IF EXISTS public.hub_recently_added;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE MATERIALIZED VIEW public.hub_recently_added AS
 SELECT l.id AS library_id,
    m.id AS media_id,
    m.type,
    m.title,
    m.year,
    m.rating,
    m.poster_path,
    m.created_at
   FROM (public.media_items m
     JOIN public.libraries l ON ((l.id = m.library_id)))
  WHERE ((m.deleted_at IS NULL) AND (m.type = ANY (ARRAY['movie'::text, 'show'::text])))
  ORDER BY m.created_at DESC;
CREATE INDEX hub_recently_added_library_id_created_at_idx ON public.hub_recently_added USING btree (library_id, created_at DESC);
CREATE UNIQUE INDEX hub_recently_added_library_id_media_id_idx ON public.hub_recently_added USING btree (library_id, media_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.content_rating_rank(rating text) RETURNS integer
    LANGUAGE plpgsql IMMUTABLE
    AS $$
BEGIN
  IF rating IS NULL OR rating = '' THEN
    RETURN 4;  -- treat unrated as most restrictive
  END IF;
  RETURN CASE rating
    WHEN 'G'     THEN 0  WHEN 'TV-Y'  THEN 0  WHEN 'TV-G' THEN 0
    WHEN 'PG'    THEN 1  WHEN 'TV-Y7' THEN 1  WHEN 'TV-PG' THEN 1
    WHEN 'PG-13' THEN 2  WHEN 'TV-14' THEN 2  WHEN 'R-15' THEN 2
    WHEN 'R'     THEN 3  WHEN 'R-17+' THEN 3
    WHEN 'NC-17' THEN 3  WHEN 'TV-MA' THEN 3  WHEN 'R+'   THEN 3  WHEN 'R-18+' THEN 3
    -- Rx (hentai): most-restrictive bucket so a TV-MA/NC-17 ceiling still blocks it.
    WHEN 'Rx'    THEN 4
    ELSE 4  -- NR, UNRATED, X, empty, etc.
  END;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS public.idx_media_items_photo_timeline;
DROP TRIGGER IF EXISTS photo_metadata_sync_taken_at ON public.photo_metadata;
DROP FUNCTION IF EXISTS public.photo_metadata_sync_taken_at();
ALTER TABLE public.media_items DROP COLUMN IF EXISTS photo_taken_at;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS public.idx_media_items_genres;
DROP INDEX IF EXISTS public.idx_media_items_unmatched;
DROP INDEX IF EXISTS public.idx_media_items_missing_art;
DROP STATISTICS IF EXISTS public.media_items_art_disc_stats;
DROP STATISTICS IF EXISTS public.media_items_art_banner_stats;
DROP STATISTICS IF EXISTS public.media_items_art_logo_stats;
DROP STATISTICS IF EXISTS public.media_items_art_thumb_stats;
DROP STATISTICS IF EXISTS public.media_items_art_fanart_stats;
DROP STATISTICS IF EXISTS public.media_items_art_poster_stats;
DROP INDEX IF EXISTS public.idx_media_items_art_disc;
DROP INDEX IF EXISTS public.idx_media_items_art_banner;
DROP INDEX IF EXISTS public.idx_media_items_art_logo;
DROP INDEX IF EXISTS public.idx_media_items_art_thumb;
DROP INDEX IF EXISTS public.idx_media_items_art_fanart;
DROP INDEX IF EXISTS public.idx_media_items_art_poster;
DROP INDEX IF EXISTS public.idx_media_items_lib_type_rating;
DROP INDEX IF EXISTS public.idx_media_items_lib_type_year;
DROP INDEX IF EXISTS public.idx_media_items_lib_type_added;
DROP INDEX IF EXISTS public.idx_media_items_lib_type_title;
-- +goose StatementEnd
