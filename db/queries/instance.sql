-- name: CountInstanceUsers :one
SELECT COUNT(*)
FROM users
JOIN accounts ON accounts.id = users.account_id
WHERE users.confirmed_at IS NOT NULL
  AND accounts.suspended_at IS NULL;

-- name: CountInstanceStatuses :one
SELECT COALESCE(SUM(account_stats.statuses_count), 0)::bigint
FROM accounts
JOIN account_stats ON account_stats.account_id = accounts.id
WHERE accounts.domain IS NULL
  AND accounts.suspended_at IS NULL;

-- name: CountInstanceDomains :one
SELECT COUNT(DISTINCT accounts.domain)
FROM accounts
WHERE accounts.domain IS NOT NULL;
