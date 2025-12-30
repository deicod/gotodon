-- name: CreateReport :one
INSERT INTO reports (
    account_id, target_account_id, status_ids, comment, category, forwarded, created_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6, now(), now()
)
RETURNING *;

-- name: AdminListReports :many
SELECT 
    r.*,
    a.username as account_username, a.domain as account_domain,
    ta.username as target_account_username, ta.domain as target_account_domain
FROM reports r
JOIN accounts a ON r.account_id = a.id
JOIN accounts ta ON r.target_account_id = ta.id
WHERE 
    (CASE WHEN @resolved::boolean THEN r.action_taken_at IS NOT NULL ELSE r.action_taken_at IS NULL END)
    AND (CASE WHEN @account_id::bigint > 0 THEN r.account_id = @account_id ELSE TRUE END)
    AND (CASE WHEN @target_account_id::bigint > 0 THEN r.target_account_id = @target_account_id ELSE TRUE END)
ORDER BY r.created_at DESC
LIMIT $1 OFFSET $2;

-- name: AdminGetReport :one
SELECT * FROM reports WHERE id = $1;

-- name: AdminResolveReport :exec
UPDATE reports 
SET action_taken_by_account_id = $2, action_taken_at = now(), updated_at = now()
WHERE id = $1;

-- name: AdminAssignReport :exec
UPDATE reports 
SET assigned_account_id = $2, updated_at = now()
WHERE id = $1;

-- name: AdminUnresolveReport :exec
UPDATE reports
SET action_taken_by_account_id = NULL, action_taken_at = NULL, updated_at = now()
WHERE id = $1;

-- name: AdminUpdateReport :one
UPDATE reports
SET category = COALESCE(sqlc.narg('category'), category),
    rule_ids = COALESCE(sqlc.narg('rule_ids'), rule_ids),
    updated_at = now()
WHERE id = $1
RETURNING *;
