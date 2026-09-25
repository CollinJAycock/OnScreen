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

-- name: ListNotifiableUserIDsForLibrary :many
-- Top-level users who may see a library: admins, everyone when the library is
-- public, and explicit grantees when it is private. Scan notifications go only
-- to these users — the library name and activity used to be broadcast to every
-- account, leaking the existence of private libraries.
SELECT u.id FROM users u
WHERE u.parent_user_id IS NULL
  AND (
        u.is_admin
     OR EXISTS (SELECT 1 FROM libraries l WHERE l.id = $1 AND l.is_private = false)
     OR EXISTS (SELECT 1 FROM library_access la WHERE la.library_id = $1 AND la.user_id = u.id)
  );
