-- +goose Up
-- +goose StatementBegin
-- Add 'photo_album' to the allowed collection types. A photo album is a
-- user-curated collection of images, surfaced via the photo-albums API.
-- Without this the INSERT in CreateCollection (Type: "photo_album") violates
-- collections_type_check and POST /api/v1/photo-albums returns HTTP 500.
-- Drop + re-add since the squashed 00001_init declares the constraint inline.
ALTER TABLE public.collections DROP CONSTRAINT IF EXISTS collections_type_check;
ALTER TABLE public.collections ADD CONSTRAINT collections_type_check
  CHECK (type = ANY (ARRAY['auto_genre', 'playlist', 'smart_playlist', 'event_folder', 'photo_album']::text[]));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.collections DROP CONSTRAINT IF EXISTS collections_type_check;
ALTER TABLE public.collections ADD CONSTRAINT collections_type_check
  CHECK (type = ANY (ARRAY['auto_genre', 'playlist', 'smart_playlist', 'event_folder']::text[]));
-- +goose StatementEnd
