-- name: CreateBookmark :exec
INSERT INTO bookmarks (account_id, status_id)
VALUES ($1, $2)
ON CONFLICT (account_id, status_id) DO NOTHING;

-- name: DeleteBookmark :exec
DELETE FROM bookmarks WHERE account_id = $1 AND status_id = $2;

-- name: GetBookmark :one
SELECT * FROM bookmarks WHERE account_id = $1 AND status_id = $2;

-- name: ListBookmarkedStatusIDs :many
SELECT status_id FROM bookmarks
WHERE account_id = $1
  AND status_id = ANY(@status_ids::bigint[]);

-- name: ListBookmarks :many
SELECT b.*, s.id as status_id
FROM bookmarks b
JOIN statuses s ON s.id = b.status_id
WHERE b.account_id = $1 AND s.deleted_at IS NULL
ORDER BY b.id DESC
LIMIT $2 OFFSET $3;

-- name: IsBookmarked :one
SELECT EXISTS(SELECT 1 FROM bookmarks WHERE account_id = $1 AND status_id = $2);
