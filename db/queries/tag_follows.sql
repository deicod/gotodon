-- Tag Follows queries

-- name: GetTagFollow :one
SELECT * FROM tag_follows WHERE account_id = $1 AND tag_id = $2;

-- name: CreateTagFollow :one
INSERT INTO tag_follows (account_id, tag_id)
VALUES ($1, $2)
RETURNING *;

-- name: DeleteTagFollow :exec
DELETE FROM tag_follows WHERE account_id = $1 AND tag_id = $2;

-- name: ListFollowedTags :many
SELECT t.* FROM tags t
JOIN tag_follows tf ON tf.tag_id = t.id
WHERE tf.account_id = $1
ORDER BY tf.created_at DESC
LIMIT $2 OFFSET $3;

-- name: CountFollowedTags :one
SELECT COUNT(*) FROM tag_follows WHERE account_id = $1;

-- name: IsFollowingTag :one
SELECT EXISTS(
    SELECT 1 FROM tag_follows WHERE account_id = $1 AND tag_id = $2
) AS is_following;

-- name: GetTagFollowersCount :one
SELECT COUNT(*) FROM tag_follows WHERE tag_id = $1;

-- Tag Trends queries

-- name: GetTagTrend :one
SELECT * FROM tag_trends WHERE tag_id = $1 AND language = $2;

-- name: UpsertTagTrend :one
INSERT INTO tag_trends (tag_id, score, rank, allowed, language)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (tag_id, language) DO UPDATE SET
    score = EXCLUDED.score,
    rank = EXCLUDED.rank,
    allowed = EXCLUDED.allowed
RETURNING *;

-- name: ListTrendingTags :many
SELECT t.*, tt.score, tt.rank FROM tags t
JOIN tag_trends tt ON tt.tag_id = t.id
WHERE tt.allowed = true AND tt.language = $1
ORDER BY tt.rank ASC
LIMIT $2 OFFSET $3;

-- name: ListAllTrendingTags :many
SELECT t.*, tt.score, tt.rank, tt.language FROM tags t
JOIN tag_trends tt ON tt.tag_id = t.id
WHERE tt.allowed = true
ORDER BY tt.rank ASC
LIMIT $1 OFFSET $2;

-- name: DeleteTagTrend :exec
DELETE FROM tag_trends WHERE tag_id = $1 AND language = $2;

-- name: ClearTagTrends :exec
DELETE FROM tag_trends;

-- name: UpdateTagTrendAllowed :one
UPDATE tag_trends SET allowed = $3
WHERE tag_id = $1 AND language = $2
RETURNING *;

-- Status Trends queries

-- name: GetStatusTrend :one
SELECT * FROM status_trends WHERE status_id = $1;

-- name: UpsertStatusTrend :one
INSERT INTO status_trends (status_id, account_id, score, rank, allowed, language)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (status_id) DO UPDATE SET
    score = EXCLUDED.score,
    rank = EXCLUDED.rank,
    allowed = EXCLUDED.allowed,
    language = EXCLUDED.language
RETURNING *;

-- name: ListTrendingStatuses :many
SELECT s.* FROM statuses s
JOIN status_trends st ON st.status_id = s.id
WHERE st.allowed = true
ORDER BY st.rank ASC
LIMIT $1 OFFSET $2;

-- name: DeleteStatusTrend :exec
DELETE FROM status_trends WHERE status_id = $1;

-- name: ClearStatusTrends :exec
DELETE FROM status_trends;

-- name: UpdateStatusTrendAllowed :one
UPDATE status_trends SET allowed = $2
WHERE status_id = $1
RETURNING *;

-- name: ListPendingTrendingStatuses :many
SELECT s.* FROM statuses s
JOIN status_trends st ON st.status_id = s.id
WHERE st.allowed = false
ORDER BY st.score DESC
LIMIT $1 OFFSET $2;
