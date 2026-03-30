-- name: InsertAuditLog :exec
INSERT INTO audit_log (user_id, action, target, detail, ip_addr)
VALUES ($1, $2, $3, $4, $5);

-- name: ListAuditLog :many
SELECT id, user_id, action, target, detail, ip_addr::TEXT AS ip_addr, created_at
FROM audit_log
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;
