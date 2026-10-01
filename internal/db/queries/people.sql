-- name: GetPersonByID :one
SELECT id, tmdb_id, name, profile_path, bio, birthday, deathday, place_of_birth, updated_at
FROM people
WHERE id = $1;

-- name: GetPersonByTMDBID :one
SELECT id, tmdb_id, name, profile_path, bio, birthday, deathday, place_of_birth, updated_at
FROM people
WHERE tmdb_id = $1;

-- name: SearchPeople :many
-- People whose name starts with the prefix. A restricted caller (non-NULL
-- library_ids and/or max_rating_rank) only finds people credited on at least
-- one item they could open — a library they have a grant on, within their
-- rating ceiling — the same scope /people/{id}/filmography applies to its
-- rows. Without it the directory listed the cast and crew of every library
-- by name and photo. Both NULL = admin: the whole table, as before.
SELECT p.id, p.tmdb_id, p.name, p.profile_path
FROM people p
WHERE LOWER(p.name) LIKE LOWER(@prefix::text) || '%'
  AND (
        (sqlc.narg('library_ids')::uuid[] IS NULL AND sqlc.narg('max_rating_rank')::int IS NULL)
     OR EXISTS (
            SELECT 1
            FROM media_credits mc
            JOIN media_items mi ON mi.id = mc.media_item_id
            WHERE mc.person_id = p.id
              AND mi.deleted_at IS NULL
              AND (sqlc.narg('library_ids')::uuid[] IS NULL
                   OR mi.library_id = ANY(sqlc.narg('library_ids')::uuid[]))
              AND (sqlc.narg('max_rating_rank')::int IS NULL
                   OR content_rating_rank(mi.content_rating) <= sqlc.narg('max_rating_rank')::int)
        )
  )
ORDER BY p.name
LIMIT @limit_n::int;

-- name: UpsertPersonByTMDB :one
INSERT INTO people (tmdb_id, name, profile_path, bio, birthday, deathday, place_of_birth, updated_at)
VALUES (@tmdb_id, @name, @profile_path, @bio, @birthday, @deathday, @place_of_birth, NOW())
ON CONFLICT (tmdb_id)
DO UPDATE SET
    name           = EXCLUDED.name,
    profile_path   = COALESCE(NULLIF(EXCLUDED.profile_path, ''), people.profile_path),
    bio            = COALESCE(NULLIF(EXCLUDED.bio, ''),          people.bio),
    birthday       = COALESCE(EXCLUDED.birthday,                 people.birthday),
    deathday       = COALESCE(EXCLUDED.deathday,                 people.deathday),
    place_of_birth = COALESCE(NULLIF(EXCLUDED.place_of_birth, ''), people.place_of_birth),
    updated_at     = NOW()
RETURNING id, tmdb_id, name, profile_path, bio, birthday, deathday, place_of_birth, updated_at;

-- name: ListCreditsForItem :many
SELECT mc.role, mc.character, mc.job, mc.ord,
       p.id AS person_id, p.tmdb_id, p.name, p.profile_path
FROM media_credits mc
JOIN people p ON p.id = mc.person_id
WHERE mc.media_item_id = $1
ORDER BY
    CASE mc.role
        WHEN 'director' THEN 0
        WHEN 'creator'  THEN 1
        WHEN 'writer'   THEN 2
        WHEN 'cast'     THEN 3
        ELSE 4
    END,
    mc.ord, p.name;

-- name: ListFilmographyForPerson :many
SELECT mc.role, mc.character, mc.job, mc.ord,
       mi.id, mi.title, mi.type, mi.year, mi.poster_path, mi.rating, mi.library_id,
       mi.content_rating
FROM media_credits mc
JOIN media_items mi ON mi.id = mc.media_item_id
WHERE mc.person_id = $1 AND mi.deleted_at IS NULL
ORDER BY mi.year DESC NULLS LAST, mi.title;

-- name: DeleteCreditsForItem :exec
DELETE FROM media_credits WHERE media_item_id = $1;

-- name: InsertCredit :exec
INSERT INTO media_credits (media_item_id, person_id, role, character, job, ord)
VALUES (@media_item_id, @person_id, @role, @character, @job, @ord)
ON CONFLICT (media_item_id, person_id, role, job) DO UPDATE SET
    character = EXCLUDED.character,
    ord       = EXCLUDED.ord;
