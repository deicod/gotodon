-- name: GetStatusByID :one
SELECT * FROM statuses WHERE id = $1 AND deleted_at IS NULL;

-- name: GetStatusByURI :one
SELECT * FROM statuses WHERE uri = $1 AND deleted_at IS NULL;

-- name: GetStatusesByIDs :many
SELECT * FROM statuses WHERE id = ANY(@ids::bigint[]) AND deleted_at IS NULL;

-- name: CreateStatus :one
INSERT INTO statuses (
    uri, text, account_id, in_reply_to_id, in_reply_to_account_id,
    reblog_of_id, sensitive, visibility, spoiler_text, language,
    conversation_id, local, application_id, reply
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14
) RETURNING *;

-- name: UpdateStatus :one
UPDATE statuses SET
    text = COALESCE(sqlc.narg('text'), text),
    sensitive = COALESCE(sqlc.narg('sensitive'), sensitive),
    spoiler_text = COALESCE(sqlc.narg('spoiler_text'), spoiler_text),
    language = COALESCE(sqlc.narg('language'), language),
    edited_at = now(),
    updated_at = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: DeleteStatus :exec
UPDATE statuses SET deleted_at = now(), updated_at = now()
WHERE id = $1;

-- name: GetStatusStats :one
SELECT * FROM status_stats WHERE status_id = $1;

-- name: CreateStatusStats :one
INSERT INTO status_stats (status_id) VALUES ($1) RETURNING *;

-- name: IncrementStatusReplies :exec
UPDATE status_stats SET replies_count = replies_count + 1, updated_at = now()
WHERE status_id = $1;

-- name: DecrementStatusReplies :exec
UPDATE status_stats SET replies_count = GREATEST(0, replies_count - 1), updated_at = now()
WHERE status_id = $1;

-- name: IncrementStatusReblogs :exec
UPDATE status_stats SET reblogs_count = reblogs_count + 1, updated_at = now()
WHERE status_id = $1;

-- name: DecrementStatusReblogs :exec
UPDATE status_stats SET reblogs_count = GREATEST(0, reblogs_count - 1), updated_at = now()
WHERE status_id = $1;

-- name: IncrementStatusFavourites :exec
UPDATE status_stats SET favourites_count = favourites_count + 1, updated_at = now()
WHERE status_id = $1;

-- name: DecrementStatusFavourites :exec
UPDATE status_stats SET favourites_count = GREATEST(0, favourites_count - 1), updated_at = now()
WHERE status_id = $1;

-- name: GetHomeTimeline :many
SELECT s.* FROM statuses s
WHERE s.deleted_at IS NULL
  AND s.account_id IN (
    SELECT target_account_id FROM follows WHERE account_id = @account_id
    UNION ALL
    SELECT @account_id
  )
  AND (s.visibility = 0 OR s.visibility = 1 OR s.account_id = @account_id)
  AND s.reblog_of_id IS NULL
ORDER BY s.id DESC
LIMIT $1 OFFSET $2;

-- name: GetPublicTimeline :many
SELECT * FROM statuses
WHERE deleted_at IS NULL
  AND visibility = 0  -- public
  AND reblog_of_id IS NULL
  AND (NOT reply OR in_reply_to_account_id = account_id)
  AND (@local::boolean = false OR local = true)
ORDER BY id DESC
LIMIT $1 OFFSET $2;

-- name: GetAccountStatuses :many
SELECT * FROM statuses
WHERE account_id = $1 
  AND deleted_at IS NULL
  AND (@exclude_replies::boolean = false OR NOT reply OR in_reply_to_account_id = account_id)
  AND (@exclude_reblogs::boolean = false OR reblog_of_id IS NULL)
  AND (@only_media::boolean = false OR ordered_media_attachment_ids IS NOT NULL)
ORDER BY id DESC
LIMIT $2 OFFSET $3;

-- name: GetStatusContext :many
SELECT * FROM statuses
WHERE deleted_at IS NULL
  AND (
    id = @status_id
    OR in_reply_to_id = @status_id
    OR id IN (
      WITH RECURSIVE ancestors AS (
        SELECT in_reply_to_id AS id FROM statuses WHERE id = @status_id AND in_reply_to_id IS NOT NULL
        UNION ALL
        SELECT s.in_reply_to_id FROM statuses s JOIN ancestors a ON s.id = a.id WHERE s.in_reply_to_id IS NOT NULL
      )
      SELECT id FROM ancestors
    )
  )
ORDER BY id;
