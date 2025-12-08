-- name: CreateFilter :one
INSERT INTO filters (
    account_id, title, context, action, expires_at
) VALUES (
    $1, $2, $3, $4, $5
) RETURNING *;

-- name: GetFilter :one
SELECT * FROM filters WHERE id = $1 AND account_id = $2;

-- name: ListFilters :many
SELECT * FROM filters 
WHERE account_id = $1
ORDER BY id DESC;

-- name: UpdateFilter :one
UPDATE filters SET
    title = COALESCE(sqlc.narg('title'), title),
    context = COALESCE(sqlc.narg('context'), context),
    action = COALESCE(sqlc.narg('action'), action),
    expires_at = sqlc.narg('expires_at'),
    updated_at = now()
WHERE id = $1 AND account_id = $2
RETURNING *;

-- name: DeleteFilter :exec
DELETE FROM filters WHERE id = $1 AND account_id = $2;

-- name: CreateFilterKeyword :one
INSERT INTO filter_keywords (filter_id, keyword, whole_word)
VALUES ($1, $2, $3)
RETURNING *;

-- name: ListFilterKeywords :many
SELECT * FROM filter_keywords WHERE filter_id = $1;

-- name: UpdateFilterKeyword :one
UPDATE filter_keywords SET
    keyword = COALESCE(sqlc.narg('keyword'), keyword),
    whole_word = COALESCE(sqlc.narg('whole_word'), whole_word),
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteFilterKeyword :exec
DELETE FROM filter_keywords WHERE id = $1;

-- name: CreateFilterStatus :one
INSERT INTO filter_statuses (filter_id, status_id)
VALUES ($1, $2)
RETURNING *;

-- name: ListFilterStatuses :many
SELECT * FROM filter_statuses WHERE filter_id = $1;

-- name: DeleteFilterStatus :exec
DELETE FROM filter_statuses WHERE id = $1;

-- name: GetFiltersForAccount :many
SELECT f.*, 
    array_agg(DISTINCT fk.id) FILTER (WHERE fk.id IS NOT NULL) as keyword_ids,
    array_agg(DISTINCT fs.id) FILTER (WHERE fs.id IS NOT NULL) as status_ids
FROM filters f
LEFT JOIN filter_keywords fk ON fk.filter_id = f.id
LEFT JOIN filter_statuses fs ON fs.filter_id = f.id
WHERE f.account_id = $1
    AND (f.expires_at IS NULL OR f.expires_at > now())
GROUP BY f.id
ORDER BY f.id DESC;
