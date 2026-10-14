-- +goose Up
-- +goose StatementBegin
-- Per-item metadata field locks. An admin's edit of a field (title, summary,
-- genres, …) adds the field's name here, and every automatic metadata write
-- (scan, enrichment, refresh, Fix Match) leaves a locked field as it is.
-- Names are media_items column names: title, sort_title, original_title,
-- year, originally_available_at, summary, tagline, rating, content_rating,
-- genres. The constant default makes this a catalog-only change on PG 11+,
-- so it doesn't rewrite the table.
ALTER TABLE public.media_items
    ADD COLUMN locked_fields text[] NOT NULL DEFAULT '{}';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.media_items DROP COLUMN IF EXISTS locked_fields;
-- +goose StatementEnd
