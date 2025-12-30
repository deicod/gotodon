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

-- name: ListSuggestedFeaturedTags :many
WITH recent_statuses AS (
    SELECT id FROM statuses
    WHERE account_id = $1 AND deleted_at IS NULL
    ORDER BY id DESC
    LIMIT 1000
)
SELECT t.* FROM tags t
JOIN statuses_tags st ON st.tag_id = t.id
JOIN recent_statuses rs ON rs.id = st.status_id
WHERE NOT EXISTS (
    SELECT 1 FROM featured_tags ft
    WHERE ft.account_id = $1
      AND (ft.tag_id = t.id OR lower(ft.name) = lower(t.name))
)
GROUP BY t.id, t.name, t.created_at, t.updated_at, t.usable, t.trendable, t.listable, t.reviewed_at, t.requested_review_at, t.last_status_at, t.max_score, t.max_score_at, t.display_name
ORDER BY COUNT(*) DESC, t.id DESC
LIMIT $2;
