-- name: UpdateUserStreamCaps :exec
UPDATE users
SET max_concurrent_streams = $2,
    max_stream_bitrate_kbps = $3
WHERE id = $1;

-- name: GetUserStreamCaps :one
-- Resolves against the EFFECTIVE OWNER: a managed profile with no caps of its
-- own inherits its parent's.
--
-- Creating a profile is open to every authenticated user and the INSERT leaves
-- these columns NULL, so a user capped at one concurrent stream could mint a
-- profile, PIN-switch into it, and fall back to the server default of five —
-- and repeat. max_concurrent_streams is also the GPU-slot control, so each
-- identity was worth another five encodes. The child keeps its own values when
-- an admin sets them, which is the intended way to give it a different budget.
--
-- COALESCE per column rather than picking a row wholesale, so a partially
-- configured child (one cap set, one not) still inherits the other.
SELECT
    COALESCE(u.max_concurrent_streams, p.max_concurrent_streams)   AS max_concurrent_streams,
    COALESCE(u.max_stream_bitrate_kbps, p.max_stream_bitrate_kbps) AS max_stream_bitrate_kbps
FROM users u
LEFT JOIN users p ON p.id = u.parent_user_id
WHERE u.id = $1;
