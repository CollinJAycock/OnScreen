-- +goose Up
-- +goose StatementBegin
-- Add 'cartoons' to the allowed library types. Cartoons (western-style animated
-- series) use the same show → season → episode hierarchy as `show`/`anime` and
-- the TMDB agent (like `show`); the distinct type only changes organization and
-- the tile icon. Mirrors how `anime` was added. Drop + re-add since the squashed
-- 00001_init declares the constraint inline.
ALTER TABLE public.libraries DROP CONSTRAINT IF EXISTS libraries_type_check;
ALTER TABLE public.libraries ADD CONSTRAINT libraries_type_check
  CHECK (type = ANY (ARRAY[
    'movie', 'show', 'music', 'photo', 'dvr', 'audiobook',
    'podcast', 'home_video', 'book', 'anime', 'manga', 'cartoons'
  ]::text[]));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.libraries DROP CONSTRAINT IF EXISTS libraries_type_check;
ALTER TABLE public.libraries ADD CONSTRAINT libraries_type_check
  CHECK (type = ANY (ARRAY[
    'movie', 'show', 'music', 'photo', 'dvr', 'audiobook',
    'podcast', 'home_video', 'book', 'anime', 'manga'
  ]::text[]));
-- +goose StatementEnd
