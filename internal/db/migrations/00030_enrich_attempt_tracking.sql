-- +goose Up
-- Track when enrichment (TMDB lookup + artwork fetch) was last attempted for
-- each item so the scanner can skip re-trying lookups that failed recently.
-- Without this, items whose titles don't match TMDB (junk release names,
-- obscure titles) get re-queried on every scan, burning API quota.
ALTER TABLE media_items
    ADD COLUMN last_enrich_attempted_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE media_items
    DROP COLUMN last_enrich_attempted_at;
