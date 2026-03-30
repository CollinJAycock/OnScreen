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
UPDATE webhook_endpoints
SET url        = $2,
    secret     = $3,
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
