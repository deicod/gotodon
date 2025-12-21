-- name: AdminListTags :many
SELECT id, name, created_at, updated_at, usable, trendable, listable, reviewed_at, requested_review_at, last_status_at, max_score, max_score_at, display_name
FROM tags
ORDER BY id DESC
LIMIT $1 OFFSET $2;

-- name: AdminGetTag :one
SELECT id, name, created_at, updated_at, usable, trendable, listable, reviewed_at, requested_review_at, last_status_at, max_score, max_score_at, display_name
FROM tags
WHERE id = $1;

-- name: AdminUpdateTag :one
UPDATE tags
SET display_name = $2,
    usable = $3,
    trendable = $4,
    listable = $5,
    reviewed_at = $6,
    updated_at = now()
WHERE id = $1
RETURNING id, name, created_at, updated_at, usable, trendable, listable, reviewed_at, requested_review_at, last_status_at, max_score, max_score_at, display_name;
