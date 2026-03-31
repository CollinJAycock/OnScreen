-- name: CreateNotification :one
INSERT INTO notifications (user_id, type, title, body, item_id)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, user_id, type, title, body, item_id, read, created_at;

-- name: ListNotifications :many
SELECT id, user_id, type, title, body, item_id, read, created_at
FROM notifications
WHERE user_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: CountUnreadNotifications :one
SELECT COUNT(*) FROM notifications
WHERE user_id = $1 AND read = false;

-- name: MarkNotificationRead :exec
UPDATE notifications SET read = true
WHERE id = $1 AND user_id = $2;

-- name: MarkAllNotificationsRead :exec
UPDATE notifications SET read = true
WHERE user_id = $1 AND read = false;

-- name: DeleteOldNotifications :exec
DELETE FROM notifications
WHERE created_at < NOW() - INTERVAL '30 days';

-- name: ListAllUserIDs :many
SELECT id FROM users WHERE parent_user_id IS NULL;
