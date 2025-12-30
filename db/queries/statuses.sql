-- name: GetStatusByID :one
SELECT * FROM statuses WHERE id = $1 AND deleted_at IS NULL;

-- name: GetStatusByURI :one
SELECT * FROM statuses WHERE uri = $1 AND deleted_at IS NULL;

-- name: GetReblogByAccountAndOriginal :one
SELECT * FROM statuses
WHERE account_id = $1 AND reblog_of_id = $2 AND deleted_at IS NULL
ORDER BY id DESC
LIMIT 1;

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

-- name: UpdateStatusQuoteApprovalPolicy :one
UPDATE statuses SET
    quote_approval_policy = $2,
    updated_at = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: UpdateStatusURL :exec
UPDATE statuses SET
    url = $2,
    updated_at = now()
WHERE id = $1;

-- name: UpdateStatusTimestamps :exec
UPDATE statuses SET
    created_at = $2,
    updated_at = $3
WHERE id = $1;

-- name: UpdateStatusInReplyTo :execrows
UPDATE statuses SET
    in_reply_to_id = $2,
    in_reply_to_account_id = $3,
    reply = true,
    updated_at = now()
WHERE id = $1 AND in_reply_to_id IS NULL;

-- name: UpdateStatusFetchedRepliesAt :exec
UPDATE statuses SET
    fetched_replies_at = $2,
    updated_at = now()
WHERE id = $1;

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

-- name: PinStatus :one
INSERT INTO status_pins (account_id, status_id)
VALUES ($1, $2)
RETURNING *;

-- name: UnpinStatus :exec
DELETE FROM status_pins WHERE account_id = $1 AND status_id = $2;

-- name: GetPinnedStatuses :many
SELECT s.* FROM statuses s
JOIN status_pins sp ON sp.status_id = s.id
WHERE sp.account_id = $1 AND s.deleted_at IS NULL
ORDER BY sp.id DESC;

-- name: GetHomeTimeline :many
SELECT s.* FROM statuses s
WHERE s.deleted_at IS NULL
  AND s.account_id IN (
    SELECT f.target_account_id FROM follows f WHERE f.account_id = @account_id
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

-- name: GetStatusAncestors :many
WITH RECURSIVE ancestors AS (
  SELECT id, in_reply_to_id FROM statuses WHERE id = @status_id AND deleted_at IS NULL
  UNION ALL
  SELECT s.id, s.in_reply_to_id FROM statuses s JOIN ancestors a ON s.id = a.in_reply_to_id WHERE s.deleted_at IS NULL
)
SELECT s.* FROM statuses s JOIN ancestors a ON s.id = a.id WHERE s.id != @status_id ORDER BY s.id;

-- name: GetStatusDescendants :many
SELECT * FROM statuses
WHERE in_reply_to_id = @status_id AND deleted_at IS NULL
ORDER BY id;

-- name: GetHashtagTimeline :many
SELECT s.* FROM statuses s
JOIN statuses_tags st ON st.status_id = s.id
JOIN tags t ON t.id = st.tag_id
WHERE s.deleted_at IS NULL
  AND s.visibility = 0  -- public
  AND lower(t.name) = lower(@tag)
  AND (@local::boolean = false OR s.local = true)
ORDER BY s.id DESC
LIMIT $1 OFFSET $2;

-- name: GetListTimeline :many
SELECT s.* FROM statuses s
JOIN list_accounts la ON la.account_id = s.account_id
WHERE la.list_id = @list_id
  AND s.deleted_at IS NULL
  AND s.reblog_of_id IS NULL
ORDER BY s.id DESC
LIMIT $1 OFFSET $2;

-- name: GetLinkTimelineStatuses :many
SELECT s.* FROM statuses s
JOIN preview_cards_statuses pcs ON pcs.status_id = s.id
WHERE pcs.preview_card_id = $1
  AND s.deleted_at IS NULL
  AND s.visibility IN (0, 1)
  AND (sqlc.narg('max_id')::bigint IS NULL OR s.id < sqlc.narg('max_id')::bigint)
  AND (sqlc.narg('since_id')::bigint IS NULL OR s.id > sqlc.narg('since_id')::bigint)
ORDER BY s.id DESC
LIMIT $2;

-- name: GetLinkTimelineStatusesMinID :many
SELECT s.* FROM statuses s
JOIN preview_cards_statuses pcs ON pcs.status_id = s.id
WHERE pcs.preview_card_id = $1
  AND s.deleted_at IS NULL
  AND s.visibility IN (0, 1)
  AND s.id > $2
  AND (sqlc.narg('max_id')::bigint IS NULL OR s.id < sqlc.narg('max_id')::bigint)
ORDER BY s.id ASC
LIMIT $3;

-- name: UpdateStatusURI :exec
UPDATE statuses SET uri = $1, updated_at = now()
WHERE id = $2;

-- name: UpdateStatusPollID :exec
UPDATE statuses SET poll_id = $2, updated_at = now()
WHERE id = $1;

-- name: CountStatusesInRange :one
SELECT COUNT(*) FROM statuses 
WHERE created_at >= $1 AND created_at < $2 AND deleted_at IS NULL;
