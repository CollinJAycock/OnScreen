-- name: CreatePlugin :one
INSERT INTO plugins (name, role, transport, endpoint_url, allowed_hosts, enabled)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetPlugin :one
SELECT * FROM plugins WHERE id = $1;

-- name: ListPlugins :many
SELECT * FROM plugins ORDER BY name;

-- name: ListEnabledPluginsByRole :many
SELECT * FROM plugins WHERE role = $1 AND enabled = TRUE ORDER BY name;

-- name: UpdatePlugin :one
UPDATE plugins
SET name          = $2,
    endpoint_url  = $3,
    allowed_hosts = $4,
    enabled       = $5,
    updated_at    = NOW()
WHERE id = $1
RETURNING *;

-- name: DeletePlugin :exec
DELETE FROM plugins WHERE id = $1;
