-- Account Statuses Cleanup Policies
-- name: GetAccountStatusesCleanupPolicy :one
SELECT * FROM account_statuses_cleanup_policies WHERE account_id = $1;

-- name: ListAccountStatusesCleanupPolicies :many
SELECT p.* FROM account_statuses_cleanup_policies p
JOIN accounts a ON a.id = p.account_id
WHERE p.enabled = true
  AND (a.domain IS NULL OR a.domain = '')
  AND p.id > @after_id
ORDER BY p.id ASC
LIMIT sqlc.arg('limit');
