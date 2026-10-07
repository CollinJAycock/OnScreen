-- +goose Up
-- +goose StatementBegin
-- Manual collections: movie and TV groupings an admin curates ("Halloween
-- night", "Best Picture winners"), shared with everyone who can see what's
-- in them. Server-owned like franchise collections (user_id NULL), and like
-- them listed only to a caller who can see at least one member.
--
-- Membership and order reuse collection_items. Two settings are new:
--   item_order      how members are listed: 'custom' (the stored positions,
--                   reordered by hand), 'release' (oldest first) or 'title'.
--   poster_item_id  the member whose poster stands for the collection; NULL
--                   uses the first member's. Cleared when that item is
--                   deleted. Not limited to manual collections by the schema,
--                   but only they let it be set.
ALTER TABLE public.collections DROP CONSTRAINT IF EXISTS collections_type_check;
ALTER TABLE public.collections ADD CONSTRAINT collections_type_check
  CHECK (type = ANY (ARRAY['auto_genre', 'playlist', 'smart_playlist', 'event_folder', 'photo_album', 'franchise', 'manual']::text[]));

ALTER TABLE public.collections
  ADD COLUMN item_order text NOT NULL DEFAULT 'custom'
    CONSTRAINT collections_item_order_check CHECK (item_order IN ('custom', 'release', 'title'));

ALTER TABLE public.collections
  ADD COLUMN poster_item_id uuid
    REFERENCES public.media_items(id) ON DELETE SET NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM public.collections WHERE type = 'manual';
ALTER TABLE public.collections DROP COLUMN IF EXISTS poster_item_id;
ALTER TABLE public.collections DROP COLUMN IF EXISTS item_order;
ALTER TABLE public.collections DROP CONSTRAINT IF EXISTS collections_type_check;
ALTER TABLE public.collections ADD CONSTRAINT collections_type_check
  CHECK (type = ANY (ARRAY['auto_genre', 'playlist', 'smart_playlist', 'event_folder', 'photo_album', 'franchise']::text[]));
-- +goose StatementEnd
