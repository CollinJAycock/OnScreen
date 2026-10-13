-- External ratings from OMDb (internal/externalratings).

-- name: ListExternalRatingCandidates :many
-- IMDb IDs of live movies and shows whose ratings are missing or due for a
-- refresh, missing ones first, then the stalest. A recent title's scores
-- still move, so it's refreshed after recent_after; an older one after
-- old_after; an ID OMDb didn't know, after missing_after.
SELECT mi.imdb_id::text AS imdb_id
FROM media_items mi
LEFT JOIN external_ratings er ON er.imdb_id = mi.imdb_id
WHERE mi.deleted_at IS NULL
  AND mi.type IN ('movie', 'show')
  AND mi.imdb_id IS NOT NULL AND mi.imdb_id <> ''
  AND (
       er.imdb_id IS NULL
    OR (NOT er.found AND er.fetched_at < @missing_before)
    OR (er.found AND COALESCE(mi.year, 0) >= @recent_year::int AND er.fetched_at < @recent_before)
    OR (er.found AND er.fetched_at < @old_before)
  )
GROUP BY mi.imdb_id, er.fetched_at
ORDER BY er.fetched_at NULLS FIRST, mi.imdb_id
LIMIT @max_ids;

-- name: UpsertExternalRatings :exec
INSERT INTO external_ratings (imdb_id, imdb_rating, imdb_votes, rotten_tomatoes, metacritic, found, fetched_at)
VALUES (@imdb_id, sqlc.narg('imdb_rating'), sqlc.narg('imdb_votes'), sqlc.narg('rotten_tomatoes'),
        sqlc.narg('metacritic'), @found, now())
ON CONFLICT (imdb_id) DO UPDATE
SET imdb_rating = EXCLUDED.imdb_rating, imdb_votes = EXCLUDED.imdb_votes,
    rotten_tomatoes = EXCLUDED.rotten_tomatoes, metacritic = EXCLUDED.metacritic,
    found = EXCLUDED.found, fetched_at = EXCLUDED.fetched_at;

-- name: GetExternalRatings :one
SELECT imdb_id, imdb_rating, imdb_votes, rotten_tomatoes, metacritic, found, fetched_at
FROM external_ratings
WHERE imdb_id = $1;

-- name: CountExternalRatingCandidates :one
-- How many movies and shows have an IMDb ID, and how many of those have
-- scores, for the settings page.
SELECT
    count(DISTINCT mi.imdb_id)::bigint AS with_imdb_id,
    count(DISTINCT er.imdb_id) FILTER (WHERE er.found)::bigint AS rated
FROM media_items mi
LEFT JOIN external_ratings er ON er.imdb_id = mi.imdb_id
WHERE mi.deleted_at IS NULL
  AND mi.type IN ('movie', 'show')
  AND mi.imdb_id IS NOT NULL AND mi.imdb_id <> '';
