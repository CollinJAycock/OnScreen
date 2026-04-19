-- name: AddFavorite :exec
INSERT INTO user_favorites (user_id, media_id)
VALUES ($1, $2)
ON CONFLICT DO NOTHING;

-- name: RemoveFavorite :exec
DELETE FROM user_favorites
WHERE user_id = $1 AND media_id = $2;

-- name: IsFavorite :one
SELECT EXISTS (
    SELECT 1 FROM user_favorites
    WHERE user_id = $1 AND media_id = $2
) AS is_favorite;

-- name: ListFavorites :many
SELECT m.id, m.library_id, m.type, m.title, m.sort_title,
       m.original_title, m.year, m.summary, m.tagline, m.rating, m.audience_rating,
       m.content_rating, m.duration_ms, m.genres, m.tags, m.tmdb_id, m.tvdb_id, m.imdb_id,
       m.musicbrainz_id, m.parent_id, m.index, m.poster_path, m.fanart_path, m.thumb_path,
       m.originally_available_at, m.created_at, m.updated_at, m.deleted_at,
       f.created_at AS favorited_at
FROM user_favorites f
JOIN media_items m ON m.id = f.media_id
WHERE f.user_id = $1
  AND m.deleted_at IS NULL
  AND (sqlc.narg('max_rating_rank')::int IS NULL OR content_rating_rank(m.content_rating) <= sqlc.narg('max_rating_rank'))
ORDER BY f.created_at DESC
LIMIT $2 OFFSET $3;

-- name: CountFavorites :one
SELECT COUNT(*) FROM user_favorites
WHERE user_id = $1;
