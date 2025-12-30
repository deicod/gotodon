-- Notification Policies queries

-- name: GetNotificationPolicy :one
SELECT * FROM notification_policies WHERE account_id = $1;

-- name: CreateNotificationPolicy :one
INSERT INTO notification_policies (
    account_id, for_not_following, for_not_followers, 
    for_new_accounts, for_private_mentions, for_limited_accounts
) VALUES (
    $1, $2, $3, $4, $5, $6
) RETURNING *;

-- name: UpdateNotificationPolicy :one
UPDATE notification_policies SET
    for_not_following = $2,
    for_not_followers = $3,
    for_new_accounts = $4,
    for_private_mentions = $5,
    for_limited_accounts = $6,
    updated_at = now()
WHERE account_id = $1
RETURNING *;

-- name: UpsertNotificationPolicy :one
INSERT INTO notification_policies (
    account_id, for_not_following, for_not_followers, 
    for_new_accounts, for_private_mentions, for_limited_accounts
) VALUES (
    $1, $2, $3, $4, $5, $6
)
ON CONFLICT (account_id) DO UPDATE SET
    for_not_following = EXCLUDED.for_not_following,
    for_not_followers = EXCLUDED.for_not_followers,
    for_new_accounts = EXCLUDED.for_new_accounts,
    for_private_mentions = EXCLUDED.for_private_mentions,
    for_limited_accounts = EXCLUDED.for_limited_accounts,
    updated_at = now()
RETURNING *;

-- name: DeleteNotificationPolicy :exec
DELETE FROM notification_policies WHERE account_id = $1;

-- Notification Permissions queries

-- name: GetNotificationPermission :one
SELECT * FROM notification_permissions 
WHERE account_id = $1 AND from_account_id = $2;

-- name: CreateNotificationPermission :one
INSERT INTO notification_permissions (account_id, from_account_id)
VALUES ($1, $2)
ON CONFLICT (account_id, from_account_id) DO NOTHING
RETURNING *;

-- name: DeleteNotificationPermission :exec
DELETE FROM notification_permissions 
WHERE account_id = $1 AND from_account_id = $2;

-- name: ListNotificationPermissions :many
SELECT * FROM notification_permissions 
WHERE account_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: HasNotificationPermission :one
SELECT EXISTS(
    SELECT 1 FROM notification_permissions 
    WHERE account_id = $1 AND from_account_id = $2
) AS has_permission;

-- Notification Requests queries

-- name: GetNotificationRequest :one
SELECT * FROM notification_requests WHERE id = $1;

-- name: GetNotificationRequestByAccounts :one
SELECT * FROM notification_requests 
WHERE account_id = $1 AND from_account_id = $2;

-- name: ListNotificationRequests :many
SELECT * FROM notification_requests 
WHERE account_id = $1
ORDER BY id DESC
LIMIT $2 OFFSET $3;

-- name: CountNotificationRequests :one
SELECT COUNT(*) FROM notification_requests WHERE account_id = $1;

-- name: SumNotificationRequestNotifications :one
SELECT COALESCE(SUM(notifications_count), 0)::bigint FROM notification_requests WHERE account_id = $1;

-- name: CreateNotificationRequest :one
INSERT INTO notification_requests (
    account_id, from_account_id, last_status_id, notifications_count
) VALUES (
    $1, $2, $3, $4
) RETURNING *;

-- name: UpdateNotificationRequest :one
UPDATE notification_requests SET
    last_status_id = $2,
    notifications_count = notifications_count + 1,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: IncrementNotificationRequestCount :one
UPDATE notification_requests SET
    notifications_count = notifications_count + 1,
    last_status_id = $2,
    updated_at = now()
WHERE account_id = $1 AND from_account_id = $3
RETURNING *;

-- name: UpsertNotificationRequest :one
INSERT INTO notification_requests (
    account_id, from_account_id, last_status_id, notifications_count
) VALUES (
    $1, $2, $3, 1
)
ON CONFLICT (account_id, from_account_id) DO UPDATE SET
    notifications_count = notification_requests.notifications_count + 1,
    last_status_id = EXCLUDED.last_status_id,
    updated_at = now()
RETURNING *;

-- name: DeleteNotificationRequest :exec
DELETE FROM notification_requests WHERE id = $1;

-- name: DeleteNotificationRequestByAccounts :exec
DELETE FROM notification_requests 
WHERE account_id = $1 AND from_account_id = $2;

-- name: AcceptNotificationRequest :exec
-- Converts a notification request to a permission (only if owned by account)
WITH deleted AS (
    DELETE FROM notification_requests nr
    WHERE nr.id = $1 AND nr.account_id = $2
    RETURNING nr.account_id, nr.from_account_id
)
INSERT INTO notification_permissions (account_id, from_account_id)
SELECT d.account_id, d.from_account_id FROM deleted d
ON CONFLICT (account_id, from_account_id) DO NOTHING;

-- name: DismissNotificationRequest :exec
DELETE FROM notification_requests WHERE id = $1 AND account_id = $2;

-- name: BulkAcceptNotificationRequests :exec
WITH deleted AS (
    DELETE FROM notification_requests nr
    WHERE nr.account_id = $1 AND nr.id = ANY(sqlc.arg(ids)::bigint[])
    RETURNING nr.account_id, nr.from_account_id
)
INSERT INTO notification_permissions (account_id, from_account_id)
SELECT d.account_id, d.from_account_id FROM deleted d
ON CONFLICT (account_id, from_account_id) DO NOTHING;

-- name: BulkDismissNotificationRequests :exec
DELETE FROM notification_requests 
WHERE account_id = $1 AND id = ANY(sqlc.arg(ids)::bigint[]);
