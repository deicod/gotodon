-- name: CreateFilter :one
INSERT INTO custom_filters (
    account_id, phrase, context, action, expires_at
) VALUES (
    $1, $2, $3, $4, $5
) RETURNING *;

-- name: GetFilter :one
SELECT * FROM custom_filters WHERE id = $1 AND account_id = $2;

-- name: ListFilters :many
SELECT * FROM custom_filters 
WHERE account_id = $1
ORDER BY id DESC;

-- name: UpdateFilter :one
UPDATE custom_filters SET
    phrase = COALESCE(sqlc.narg('phrase'), phrase),
    context = COALESCE(sqlc.narg('context'), context),
    action = COALESCE(sqlc.narg('action'), action),
    expires_at = sqlc.narg('expires_at'),
    updated_at = now()
WHERE id = $1 AND account_id = $2
RETURNING *;

-- name: DeleteFilter :exec
DELETE FROM custom_filters WHERE id = $1 AND account_id = $2;

-- name: CreateFilterKeyword :one
INSERT INTO custom_filter_keywords (custom_filter_id, keyword, whole_word)
VALUES ($1, $2, $3)
RETURNING *;

-- name: ListFilterKeywords :many
SELECT * FROM custom_filter_keywords WHERE custom_filter_id = $1;

-- name: UpdateFilterKeyword :one
UPDATE custom_filter_keywords SET
    keyword = COALESCE(sqlc.narg('keyword'), keyword),
    whole_word = COALESCE(sqlc.narg('whole_word'), whole_word),
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteFilterKeyword :exec
DELETE FROM custom_filter_keywords WHERE id = $1;

-- name: CreateFilterStatus :one
INSERT INTO custom_filter_statuses (custom_filter_id, status_id)
VALUES ($1, $2)
RETURNING *;

-- name: ListFilterStatuses :many
SELECT * FROM custom_filter_statuses WHERE custom_filter_id = $1;

-- name: DeleteFilterStatus :exec
DELETE FROM custom_filter_statuses WHERE id = $1;

-- name: GetFiltersForAccount :many
SELECT f.*, 
    array_agg(DISTINCT fk.id) FILTER (WHERE fk.id IS NOT NULL) as keyword_ids,
    array_agg(DISTINCT fs.id) FILTER (WHERE fs.id IS NOT NULL) as status_ids
FROM custom_filters f
LEFT JOIN custom_filter_keywords fk ON fk.custom_filter_id = f.id
LEFT JOIN custom_filter_statuses fs ON fs.custom_filter_id = f.id
WHERE f.account_id = $1
    AND (f.expires_at IS NULL OR f.expires_at > now())
GROUP BY f.id
ORDER BY f.id DESC;
