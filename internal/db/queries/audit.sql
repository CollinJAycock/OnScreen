-- name: InsertAuditLog :exec
INSERT INTO audit_log (user_id, action, target, detail, ip_addr)
VALUES ($1, $2, $3, $4, $5);

-- name: ListAuditLog :many
SELECT id, user_id, action, target, detail, COALESCE(ip_addr::TEXT, '')::TEXT AS ip_addr, created_at
FROM audit_log
-- , id DESC tiebreaker: created_at can tie for near-simultaneous writes, so
-- OFFSET paging over the audit trail needs a unique key to avoid skip/duplicate.
ORDER BY created_at DESC, id DESC
LIMIT $1 OFFSET $2;
