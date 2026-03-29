-- name: CountInstanceUsers :one
SELECT COUNT(*)
FROM users
JOIN accounts ON accounts.id = users.account_id
WHERE users.confirmed_at IS NOT NULL
  AND accounts.suspended_at IS NULL;

-- name: CountActiveUsers :one
SELECT COUNT(*)
FROM users
JOIN accounts ON accounts.id = users.account_id
WHERE users.confirmed_at IS NOT NULL
  AND accounts.suspended_at IS NULL
  AND COALESCE(users.current_sign_in_at, users.last_sign_in_at) >= NOW() - INTERVAL '30 days';

-- name: CountActiveUsersHalfyear :one
SELECT COUNT(*)
FROM users
JOIN accounts ON accounts.id = users.account_id
WHERE users.confirmed_at IS NOT NULL
  AND accounts.suspended_at IS NULL
  AND COALESCE(users.current_sign_in_at, users.last_sign_in_at) >= NOW() - INTERVAL '24 weeks';

-- name: CountNodeInfoActiveUsersMonth :one
SELECT COUNT(DISTINCT login_activities.user_id)
FROM login_activities
JOIN users ON users.id = login_activities.user_id
JOIN accounts ON accounts.id = users.account_id
WHERE login_activities.success = true
  AND login_activities.created_at >= NOW() - INTERVAL '4 weeks'
  AND users.confirmed_at IS NOT NULL
  AND accounts.suspended_at IS NULL;

-- name: CountNodeInfoActiveUsersHalfyear :one
SELECT COUNT(DISTINCT login_activities.user_id)
FROM login_activities
JOIN users ON users.id = login_activities.user_id
JOIN accounts ON accounts.id = users.account_id
WHERE login_activities.success = true
  AND login_activities.created_at >= NOW() - INTERVAL '24 weeks'
  AND users.confirmed_at IS NOT NULL
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
