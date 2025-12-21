-- name: CreateFeaturedTag :one
INSERT INTO featured_tags (account_id, tag_id, name, statuses_count, last_status_at)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: DeleteFeaturedTag :exec
DELETE FROM featured_tags
WHERE id = $1 AND account_id = $2;

-- name: ListFeaturedTags :many
SELECT * FROM featured_tags
WHERE account_id = $1
ORDER BY id DESC;

-- name: GetFeaturedTagByTagID :one
SELECT * FROM featured_tags
WHERE account_id = $1 AND tag_id = $2;

-- name: CountFeaturedTags :one
SELECT count(*) FROM featured_tags WHERE account_id = $1;

-- name: DeleteFeaturedTagByTagID :exec
DELETE FROM featured_tags
WHERE account_id = $1 AND tag_id = $2;

