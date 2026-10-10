-- name: InsertNotification :one
INSERT INTO notifications (user_id, type, payload)
VALUES ($1, $2, $3)
RETURNING *;

-- name: ListNotifications :many
SELECT id, type, payload, read_at, created_at
FROM notifications
WHERE user_id = sqlc.arg(user_id)
  AND (
      NOT sqlc.arg(has_cursor)::boolean
      OR (created_at, id) < (sqlc.arg(cursor_at)::timestamptz, sqlc.arg(cursor_id)::uuid)
  )
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit);

-- name: CountUnreadNotifications :one
SELECT count(*)::int AS unread_count
FROM notifications
WHERE user_id = $1 AND read_at IS NULL;

-- name: MarkNotificationsRead :execrows
UPDATE notifications
SET read_at = now()
WHERE user_id = sqlc.arg(user_id)
  AND read_at IS NULL
  AND id = ANY (sqlc.arg(ids)::uuid[]);

-- name: MarkAllNotificationsRead :execrows
UPDATE notifications
SET read_at = now()
WHERE user_id = $1 AND read_at IS NULL;

-- name: DeleteOldReadNotifications :execrows
DELETE FROM notifications
WHERE read_at IS NOT NULL AND read_at < $1;
