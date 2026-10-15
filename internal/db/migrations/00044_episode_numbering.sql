-- +goose Up
-- +goose StatementBegin
-- Multi-episode files and the episode order a show is numbered in.
--
-- media_items.index_end   The last episode a multi-episode file covers:
--                         "Show S01E01-E02.mkv" is ONE episode item with
--                         index 1, index_end 2 (Jellyfin's model). NULL for
--                         a single episode. Everything that counts episodes
--                         (season counts, unwatched badges, Next Up, the
--                         owned-episode counts season requests read) treats
--                         index..index_end as present.
-- media_items.episode_order
--                         A show's own episode order: 'aired', 'dvd' or
--                         'absolute'. NULL follows the library default.
-- libraries.episode_order The library default for its shows. 'aired' keeps
--                         the provider's broadcast numbering, which is what
--                         every library used before this migration.
ALTER TABLE public.media_items
  ADD COLUMN index_end integer
    CONSTRAINT media_items_index_end_check CHECK (index_end IS NULL OR index_end > 0),
  ADD COLUMN episode_order text
    CONSTRAINT media_items_episode_order_check CHECK (episode_order IN ('aired', 'dvd', 'absolute'));

ALTER TABLE public.libraries
  ADD COLUMN episode_order text NOT NULL DEFAULT 'aired'
    CONSTRAINT libraries_episode_order_check CHECK (episode_order IN ('aired', 'dvd', 'absolute'));
-- +goose StatementEnd

-- +goose StatementBegin
-- episode_ancestry gains episode_span: how many episodes the row covers (2
-- for an E01-E02 file), so show / season rollups count episodes rather than
-- files. A range whose end doesn't lie past its start counts as one.
CREATE OR REPLACE VIEW public.episode_ancestry AS
SELECT e.parent_id AS container_id, e.id AS episode_id, e.content_rating,
       (CASE WHEN e.index_end > e.index THEN e.index_end - e.index + 1 ELSE 1 END)::integer AS episode_span
FROM public.media_items e
WHERE e.type = 'episode' AND e.deleted_at IS NULL AND e.parent_id IS NOT NULL
UNION ALL
SELECT s.parent_id AS container_id, e.id AS episode_id, e.content_rating,
       (CASE WHEN e.index_end > e.index THEN e.index_end - e.index + 1 ELSE 1 END)::integer AS episode_span
FROM public.media_items s
JOIN public.media_items e
  ON e.parent_id = s.id AND e.type = 'episode' AND e.deleted_at IS NULL
WHERE s.type = 'season' AND s.deleted_at IS NULL AND s.parent_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- A view can't drop a column in place; media_watch_bucket's SQL body isn't
-- dependency-tracked, so drop and recreate the 00023 shape.
DROP VIEW IF EXISTS public.episode_ancestry;
CREATE VIEW public.episode_ancestry AS
SELECT e.parent_id AS container_id, e.id AS episode_id, e.content_rating
FROM public.media_items e
WHERE e.type = 'episode' AND e.deleted_at IS NULL AND e.parent_id IS NOT NULL
UNION ALL
SELECT s.parent_id AS container_id, e.id AS episode_id, e.content_rating
FROM public.media_items s
JOIN public.media_items e
  ON e.parent_id = s.id AND e.type = 'episode' AND e.deleted_at IS NULL
WHERE s.type = 'season' AND s.deleted_at IS NULL AND s.parent_id IS NOT NULL;

ALTER TABLE public.libraries DROP COLUMN IF EXISTS episode_order;
ALTER TABLE public.media_items
  DROP COLUMN IF EXISTS episode_order,
  DROP COLUMN IF EXISTS index_end;
-- +goose StatementEnd
