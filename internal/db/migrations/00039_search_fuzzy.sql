-- +goose Up
-- pg_trgm enables similarity matching on title fields so search tolerates
-- typos and partial matches that the english-stemmed FTS column misses
-- (e.g. "matrx" → "Matrix", "amelie" → "Amélie", foreign titles whose
-- tokens don't stem under the english dictionary).
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- Trigram GIN indexes back the `%` similarity operator on the two title
-- columns we search against. We do NOT index summary — its size would
-- bloat the index without meaningfully improving result quality, and
-- summary remains covered by the FTS search_vector.
CREATE INDEX IF NOT EXISTS idx_media_items_title_trgm
    ON media_items USING gin (title gin_trgm_ops)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_media_items_original_title_trgm
    ON media_items USING gin (original_title gin_trgm_ops)
    WHERE deleted_at IS NULL AND original_title IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_media_items_original_title_trgm;
DROP INDEX IF EXISTS idx_media_items_title_trgm;
DROP EXTENSION IF EXISTS pg_trgm;
