-- +goose Up
-- +goose StatementBegin
-- Manual collections, phase 2: a home-screen row, an uploaded cover, smart
-- (rule-based) membership and collections imported from Kodi NFO files.
--
-- promoted        An admin's "Show on Home": the collection becomes a home
--                 row for everyone who can see one of its members (each user
--                 can still hide or move it in their home layout, as a
--                 collection:<id> row).
-- source,         Where an imported collection came from: source 'nfo' with
-- source_key      'set:<name>' or 'tag:<name>' (lower-cased). The scanner
--                 finds-or-creates by that pair, so a re-scan joins the same
--                 collection instead of making another.
-- rules           (existing column) on a manual collection makes it smart:
--                 its members are refreshed from the rules (the smart
--                 playlist grammar) when they're saved and by the hourly
--                 smart_collections task, so the visibility, cover and count
--                 rules that read collection_items apply unchanged.
ALTER TABLE public.collections
  ADD COLUMN promoted boolean NOT NULL DEFAULT false,
  ADD COLUMN source text,
  ADD COLUMN source_key text;

CREATE UNIQUE INDEX collections_source_key
  ON public.collections (source, source_key)
  WHERE source IS NOT NULL;

CREATE INDEX collections_promoted
  ON public.collections (id)
  WHERE promoted;

-- An uploaded cover, kept in the database so every node serves it and a
-- backup carries it. The image is re-encoded server-side (bounded decode,
-- resized JPEG), so only that output is stored.
CREATE TABLE public.collection_posters (
    collection_id uuid PRIMARY KEY
        REFERENCES public.collections(id) ON DELETE CASCADE,
    content_type text NOT NULL,
    data bytea NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

-- Which collections a library's scan imports from movie.nfo files: none,
-- <set> only, or <set> and <tag>.
ALTER TABLE public.libraries
  ADD COLUMN nfo_collections text NOT NULL DEFAULT 'off'
    CONSTRAINT libraries_nfo_collections_check CHECK (nfo_collections IN ('off', 'sets', 'sets_and_tags'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.libraries DROP COLUMN IF EXISTS nfo_collections;
DROP TABLE IF EXISTS public.collection_posters;
DROP INDEX IF EXISTS public.collections_promoted;
DROP INDEX IF EXISTS public.collections_source_key;
ALTER TABLE public.collections
  DROP COLUMN IF EXISTS source_key,
  DROP COLUMN IF EXISTS source,
  DROP COLUMN IF EXISTS promoted;
-- +goose StatementEnd
