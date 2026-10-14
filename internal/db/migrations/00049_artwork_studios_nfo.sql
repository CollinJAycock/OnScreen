-- +goose Up
-- +goose StatementBegin
-- Extra artwork, studios / networks, and NFO writing.
--
-- logo_path / banner_path / disc_art_path sit beside poster_path and
-- fanart_path and hold the same thing: a library-relative path to an image
-- next to the media (ADR-006), served through /artwork/*. A logo is a
-- transparent "clear logo" PNG, a banner a wide strip, disc art a disc or
-- CD face. Filled from Kodi-named files next to the media, then fanart.tv,
-- then (banners, anime) AniList.
ALTER TABLE public.media_items
    ADD COLUMN logo_path     text,
    ADD COLUMN banner_path   text,
    ADD COLUMN disc_art_path text;

-- save_nfo: write Kodi-compatible movie.nfo / tvshow.nfo / episode NFOs next
-- to the media after enrichment, a manual edit or Fix Match. Off by default:
-- writing into the media folders is the operator's call.
ALTER TABLE public.libraries
    ADD COLUMN save_nfo boolean DEFAULT false NOT NULL;

-- studios: production companies (kind 'studio', movies) and TV networks
-- (kind 'network', shows). One row per (kind, name) whatever its case, so
-- TMDB's "Warner Bros. Pictures" and an NFO's "warner bros. pictures" meet.
-- tmdb_id is the TMDB company / network id when the name came from TMDB.
-- logo_path is reserved for a studio logo (unused for now).
CREATE TABLE public.studios (
    id         uuid DEFAULT gen_random_uuid() PRIMARY KEY,
    name       text NOT NULL,
    kind       text NOT NULL,
    tmdb_id    integer,
    logo_path  text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT studios_kind_check CHECK (kind IN ('studio', 'network')),
    CONSTRAINT studios_name_check CHECK (btrim(name) <> '')
);
CREATE UNIQUE INDEX idx_studios_kind_name ON public.studios USING btree (kind, lower(name));

-- media_item_studios: which studios made an item, in the provider's order
-- (position 0 is the main one). Rewritten as a whole on each enrichment.
CREATE TABLE public.media_item_studios (
    media_item_id uuid NOT NULL REFERENCES public.media_items(id) ON DELETE CASCADE,
    studio_id     uuid NOT NULL REFERENCES public.studios(id) ON DELETE CASCADE,
    position      integer DEFAULT 0 NOT NULL,
    PRIMARY KEY (media_item_id, studio_id)
);
CREATE INDEX idx_media_item_studios_studio ON public.media_item_studios USING btree (studio_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS public.media_item_studios;
DROP TABLE IF EXISTS public.studios;
ALTER TABLE public.libraries DROP COLUMN IF EXISTS save_nfo;
ALTER TABLE public.media_items
    DROP COLUMN IF EXISTS disc_art_path,
    DROP COLUMN IF EXISTS banner_path,
    DROP COLUMN IF EXISTS logo_path;
-- +goose StatementEnd
