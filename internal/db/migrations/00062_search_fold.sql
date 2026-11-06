-- +goose Up
-- +goose StatementBegin
-- Search wanted an almost exact title: full-text search matches whole words
-- only (a word still being typed matched nothing), and the trigram match
-- compared the query with the WHOLE title, so a short query against a long
-- title ("star" vs "Star Wars: The Empire Strikes Back") fell under the
-- similarity cut-off. SearchMediaItems now also matches a title that
-- contains the query, and one whose words the query resembles
-- (word_similarity), both on the folded title: lower case, accents removed,
-- so "amelie" finds "Amélie".
--
-- search_fold is IMMUTABLE so it can back a stored generated column
-- (unaccent itself is only STABLE: its dictionary could change, which
-- rewriting the column would then pick up).
CREATE FUNCTION public.search_fold(t text) RETURNS text
    LANGUAGE sql IMMUTABLE PARALLEL SAFE
    AS $$ SELECT lower(public.unaccent(t)) $$;
-- +goose StatementEnd

-- +goose StatementBegin
-- The query folded the same way, with LIKE's wildcards (% and _) and its
-- escape character escaped, for "title contains / starts with the query".
CREATE FUNCTION public.search_like(q text) RETURNS text
    LANGUAGE sql IMMUTABLE PARALLEL SAFE
    AS $$ SELECT replace(replace(replace(public.search_fold(q), '\', '\\'), '%', '\%'), '_', '\_') $$;
-- +goose StatementEnd

-- The folded title, stored: computed per row at query time, unaccent made a
-- broad query (one most titles contain) take seconds on a 120k-item library
-- and kept the planner off the indexes.
ALTER TABLE public.media_items
    ADD COLUMN title_fold text GENERATED ALWAYS AS (public.search_fold(title)) STORED;

-- Backs title_fold LIKE '%…%' and the <% word-similarity operator.
CREATE INDEX idx_media_items_title_fold_trgm ON public.media_items
    USING gin (title_fold public.gin_trgm_ops)
    WHERE deleted_at IS NULL;

-- +goose Down
DROP INDEX IF EXISTS public.idx_media_items_title_fold_trgm;
ALTER TABLE public.media_items DROP COLUMN IF EXISTS title_fold;
DROP FUNCTION IF EXISTS public.search_like(text);
DROP FUNCTION IF EXISTS public.search_fold(text);
