-- name: UpdateUserStreamCaps :exec
UPDATE users
SET max_concurrent_streams = $2,
    max_stream_bitrate_kbps = $3
WHERE id = $1;

-- name: GetUserStreamCaps :one
SELECT max_concurrent_streams, max_stream_bitrate_kbps
FROM users
WHERE id = $1;
