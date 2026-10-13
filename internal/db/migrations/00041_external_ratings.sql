-- +goose Up
-- +goose StatementBegin
-- IMDb, Rotten Tomatoes (critics) and Metacritic scores, fetched from OMDb
-- (internal/externalratings) by the daily external_ratings task. Keyed by
-- IMDb ID, not item: every copy of a title (an HD and a 4K library) shares
-- one row and one lookup. NULL scores are ones OMDb doesn't have.
--
--   found       false when OMDb doesn't know the ID; retried less often
--   fetched_at  when the row was last refreshed, for picking stale rows
CREATE TABLE public.external_ratings (
    imdb_id         text NOT NULL,
    imdb_rating     double precision,
    imdb_votes      integer,
    rotten_tomatoes smallint,
    metacritic      smallint,
    found           boolean DEFAULT true NOT NULL,
    fetched_at      timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT external_ratings_pkey PRIMARY KEY (imdb_id),
    CONSTRAINT external_ratings_imdb_rating_check CHECK (imdb_rating IS NULL OR imdb_rating BETWEEN 0 AND 10),
    CONSTRAINT external_ratings_rotten_tomatoes_check CHECK (rotten_tomatoes IS NULL OR rotten_tomatoes BETWEEN 0 AND 100),
    CONSTRAINT external_ratings_metacritic_check CHECK (metacritic IS NULL OR metacritic BETWEEN 0 AND 100)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS public.external_ratings;
-- +goose StatementEnd
