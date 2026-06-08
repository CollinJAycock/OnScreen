-- name: UpsertUserRating :exec
INSERT INTO user_ratings (user_id, media_item_id, score)
VALUES ($1, $2, $3)
ON CONFLICT (user_id, media_item_id)
DO UPDATE SET score = EXCLUDED.score, updated_at = now();

-- name: GetUserRating :one
SELECT score
FROM user_ratings
WHERE user_id = $1 AND media_item_id = $2;

-- name: DeleteUserRating :exec
DELETE FROM user_ratings
WHERE user_id = $1 AND media_item_id = $2;

-- name: GetCommunityRating :one
SELECT COALESCE(AVG(score), 0)::double precision AS average,
       COUNT(*)::bigint AS count
FROM user_ratings
WHERE media_item_id = $1;
