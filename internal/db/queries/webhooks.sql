-- name: ListWebhookEndpoints :many
SELECT id, url, secret, events, enabled, created_at, updated_at
FROM webhook_endpoints
ORDER BY created_at;

-- name: GetWebhookEndpoint :one
SELECT id, url, secret, events, enabled, created_at, updated_at
FROM webhook_endpoints
WHERE id = $1;

-- name: ListEnabledWebhookEndpointsForEvent :many
SELECT id, url, secret, events, enabled, created_at, updated_at
FROM webhook_endpoints
WHERE enabled = true AND $1::text = ANY(events);

-- name: CreateWebhookEndpoint :one
INSERT INTO webhook_endpoints (url, secret, events)
VALUES ($1, $2, $3)
RETURNING id, url, secret, events, enabled, created_at, updated_at;

-- name: UpdateWebhookEndpoint :one
-- secret: a NULL argument KEEPS the stored signing secret. Callers pass NULL
-- whenever the request omitted a secret (the UI's "leave blank to keep
-- current"); the old `secret = $3` silently wiped the secret on every edit and
-- on the enable/disable toggle, after which deliveries went out unsigned.
UPDATE webhook_endpoints
SET url        = $2,
    secret     = COALESCE($3, secret),
    events     = $4,
    enabled    = $5,
    updated_at = NOW()
WHERE id = $1
RETURNING id, url, secret, events, enabled, created_at, updated_at;

-- name: DeleteWebhookEndpoint :exec
DELETE FROM webhook_endpoints WHERE id = $1;

-- name: CreateWebhookFailure :one
INSERT INTO webhook_failures (endpoint_id, url, payload, last_error, attempt_count)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, endpoint_id, url, payload, last_error, attempt_count, failed_at;

-- name: ListWebhookFailures :many
SELECT id, endpoint_id, url, payload, last_error, attempt_count, failed_at
FROM webhook_failures
ORDER BY failed_at DESC
LIMIT $1 OFFSET $2;
