-- +goose Up
-- +goose StatementBegin
-- Per-(user, item) star rating, 0-10 (the float allows half-star steps when a
-- 5-star UI maps each star to 2 points). Distinct from media_items.rating and
-- media_items.audience_rating, which hold the external provider's critic +
-- audience scores. The "community" rating shown in the UI is computed over this
-- table (AVG/COUNT) at read time — it's what *your* users think.
CREATE TABLE public.user_ratings (
    user_id uuid NOT NULL,
    media_item_id uuid NOT NULL,
    score double precision NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT user_ratings_pkey PRIMARY KEY (user_id, media_item_id),
    CONSTRAINT user_ratings_score_check CHECK (score >= 0 AND score <= 10)
);
CREATE INDEX user_ratings_media_item_id_idx ON public.user_ratings (media_item_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS public.user_ratings;
-- +goose StatementEnd
