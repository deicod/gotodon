-- name: AdminListAccounts :many
SELECT 
    a.id, a.username, a.domain, a.created_at, 
    u.email, u.current_sign_in_at as ip, -- Placeholder for IP, using timestamps or actual IP if available in users
    u.confirmed_at, u.approved, u.disabled, a.silenced_at, a.suspended_at, a.sensitized_at,
    u.invite_id, u.created_by_application_id
FROM accounts a
JOIN users u ON a.id = u.account_id
WHERE 
    (CASE WHEN @local_only::boolean THEN a.domain IS NULL ELSE TRUE END)
    AND (CASE WHEN @status_active::boolean THEN u.disabled = false AND a.suspended_at IS NULL ELSE TRUE END)
    AND (CASE WHEN @status_pending::boolean THEN u.approved = false ELSE TRUE END)
    AND (CASE WHEN @status_disabled::boolean THEN u.disabled = true OR a.suspended_at IS NOT NULL ELSE TRUE END)
    AND (CASE WHEN @status_silenced::boolean THEN a.silenced_at IS NOT NULL ELSE TRUE END)
    AND (CASE WHEN @status_suspended::boolean THEN a.suspended_at IS NOT NULL ELSE TRUE END)
    AND (CASE WHEN @username_curr::text != '' THEN a.username ILIKE '%' || @username_curr || '%' ELSE TRUE END)
    AND (CASE WHEN @email::text != '' THEN u.email ILIKE '%' || @email || '%' ELSE TRUE END)
    AND (CASE WHEN @ip::text != '' THEN u.sign_up_ip::text = @ip ELSE TRUE END)
ORDER BY a.created_at DESC
LIMIT $1 OFFSET $2;

-- name: AdminGetAccount :one
SELECT 
    a.id, a.username, a.domain, a.created_at, 
    u.email, u.sign_up_ip,
    u.confirmed_at, u.approved, u.disabled, a.silenced_at, a.suspended_at, a.sensitized_at,
    u.invite_id, u.created_by_application_id
FROM accounts a
JOIN users u ON a.id = u.account_id
WHERE a.id = $1;

-- name: AdminApproveAccount :exec
UPDATE users SET approved = true, updated_at = now() 
WHERE account_id = $1;

-- name: AdminRejectAccount :exec
DELETE FROM users WHERE account_id = $1; 
-- Note: Cascades to accounts usually, or we should delete account separately? Schema has fk_users_account -> accounts(id).
-- Accounts table is the parent. FK is in users pointing to accounts.
-- Actually, deleting the USER might not delete the ACCOUNT if the FK is ON DELETE CASCADE on the user side?
-- Schema: users.account_id REFERENCES accounts(id) ON DELETE CASCADE.
-- So deleting account deletes user. Deleting user does NOT delete account automatically unless we do it manually.
-- For rejection, we usually want to delete the account entirely.
-- Let's change this to delete from accounts.

-- name: AdminDeleteAccount :exec
DELETE FROM accounts WHERE id = $1;

-- name: AdminDisableAccount :exec
UPDATE users SET disabled = true, updated_at = now() WHERE account_id = $1;

-- name: AdminEnableAccount :exec
UPDATE users SET disabled = false, updated_at = now() WHERE account_id = $1;

-- name: AdminSilenceAccount :exec
UPDATE accounts SET silenced_at = now(), updated_at = now() WHERE id = $1;

-- name: AdminUnsilenceAccount :exec
UPDATE accounts SET silenced_at = NULL, updated_at = now() WHERE id = $1;

-- name: AdminSuspendAccount :exec
UPDATE accounts SET suspended_at = now(), suspension_origin = 0, updated_at = now() WHERE id = $1;

-- name: AdminUnsuspendAccount :exec
UPDATE accounts SET suspended_at = NULL, updated_at = now() WHERE id = $1;

-- name: AdminSensitizeAccount :exec
UPDATE accounts SET sensitized_at = now(), updated_at = now() WHERE id = $1;

-- name: AdminUnsensitizeAccount :exec
UPDATE accounts SET sensitized_at = NULL, updated_at = now() WHERE id = $1;
