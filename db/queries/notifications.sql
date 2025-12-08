-- name: CreateNotification :one
INSERT INTO notifications (
    account_id, from_account_id, activity_type, activity_id, type
) VALUES (
    $1, $2, $3, $4, $5
) RETURNING *;

-- name: GetNotification :one
SELECT * FROM notifications WHERE id = $1 AND account_id = $2;

-- name: ListNotifications :many
SELECT * FROM notifications
WHERE account_id = $1
  AND ($4::text[] IS NULL OR type = ANY($4::text[]))
  AND ($5::text[] IS NULL OR type != ALL($5::text[]))
ORDER BY id DESC
LIMIT $2 OFFSET $3;

-- name: DeleteNotification :exec
DELETE FROM notifications WHERE id = $1 AND account_id = $2;

-- name: ClearNotifications :exec
DELETE FROM notifications WHERE account_id = $1;

-- name: MarkNotificationsRead :exec
UPDATE notifications SET read = true WHERE account_id = $1 AND read = false;

-- name: CountUnreadNotifications :one
SELECT COUNT(*) FROM notifications WHERE account_id = $1 AND read = false;
