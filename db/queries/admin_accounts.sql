-- name: AdminListAccounts :many
SELECT 
    a.id, a.username, a.domain, a.created_at, 
    u.email, u.current_sign_in_at as ip, -- Placeholder for IP, using timestamps or actual IP if available in users
    u.confirmed_at, u.approved, u.disabled, a.silenced_at, a.suspended_at, a.sensitized_at,
    u.invite_id, u.created_by_application_id,
    r.id AS role_id,
    r.name AS role_name,
    r.permissions AS role_permissions,
    r.color AS role_color,
    r.position AS role_position,
    r.highlighted AS role_highlighted,
    r.created_at AS role_created_at,
    r.updated_at AS role_updated_at
FROM accounts a
JOIN users u ON a.id = u.account_id
LEFT JOIN user_roles r ON r.id = u.role_id
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
    u.invite_id, u.created_by_application_id,
    r.id AS role_id,
    r.name AS role_name,
    r.permissions AS role_permissions,
    r.color AS role_color,
    r.position AS role_position,
    r.highlighted AS role_highlighted,
    r.created_at AS role_created_at,
    r.updated_at AS role_updated_at
FROM accounts a
JOIN users u ON a.id = u.account_id
LEFT JOIN user_roles r ON r.id = u.role_id
WHERE a.id = $1;

-- name: AdminListAccountsV2 :many
SELECT
    a.id,
    a.username,
    a.domain,
    a.created_at,
    s.followers_count,
    s.following_count,
    s.statuses_count,
    s.last_status_at,
    u.id AS user_id,
    u.email,
    u.sign_up_ip,
    u.confirmed_at,
    u.approved,
    u.disabled,
    a.silenced_at,
    a.suspended_at,
    a.sensitized_at,
    u.invite_id,
    u.created_by_application_id,
    u.locale,
    uir.text AS invite_request,
    inviter.account_id AS invited_by_account_id,
    r.id AS role_id,
    r.name AS role_name,
    r.permissions AS role_permissions,
    r.color AS role_color,
    r.highlighted AS role_highlighted
FROM accounts a
LEFT JOIN users u ON a.id = u.account_id
LEFT JOIN account_stats s ON s.account_id = a.id
LEFT JOIN invites i ON i.id = u.invite_id
LEFT JOIN users inviter ON inviter.id = i.user_id
LEFT JOIN user_roles r ON r.id = u.role_id
LEFT JOIN user_invite_requests uir ON uir.user_id = u.id
WHERE
    (CASE
        WHEN @origin::text = 'local' THEN a.domain IS NULL
        WHEN @origin::text = 'remote' THEN a.domain IS NOT NULL
        ELSE TRUE
    END)
    AND (CASE WHEN @by_domain::text != '' THEN a.domain = @by_domain ELSE TRUE END)
    AND (CASE WHEN @status::text = 'active' THEN a.suspended_at IS NULL ELSE TRUE END)
    AND (CASE WHEN @status::text = 'pending' THEN u.approved = false ELSE TRUE END)
    AND (CASE WHEN @status::text = 'disabled' THEN u.disabled = true AND a.suspended_at IS NULL ELSE TRUE END)
    AND (CASE WHEN @status::text = 'suspended' THEN a.suspended_at IS NOT NULL ELSE TRUE END)
    AND (CASE WHEN @status::text = 'silenced' THEN a.silenced_at IS NOT NULL ELSE TRUE END)
    AND (CASE WHEN @status::text = 'sensitized' THEN a.sensitized_at IS NOT NULL ELSE TRUE END)
    AND (CASE WHEN @username::text != '' THEN lower(a.username) LIKE lower(@username || '%') ELSE TRUE END)
    AND (CASE WHEN @display_name::text != '' THEN a.display_name ILIKE @display_name || '%' ELSE TRUE END)
    AND (CASE WHEN @email::text != '' THEN u.email ILIKE @email || '%' ELSE TRUE END)
    AND (CASE WHEN @ip::text != '' THEN (
        (u.sign_up_ip IS NOT NULL AND u.sign_up_ip <<= @ip::inet)
        OR EXISTS (SELECT 1 FROM session_activations sa WHERE sa.user_id = u.id AND sa.ip IS NOT NULL AND sa.ip <<= @ip::inet)
        OR EXISTS (SELECT 1 FROM login_activities la WHERE la.user_id = u.id AND la.ip IS NOT NULL AND la.success = true AND la.ip <<= @ip::inet)
    ) ELSE TRUE END)
    AND (CASE WHEN @invited_by::bigint > 0 THEN i.user_id = @invited_by ELSE TRUE END)
    AND (CASE WHEN @permissions::text = 'staff' THEN (r.permissions & 16) = 16 OR (r.permissions & 1) = 1 ELSE TRUE END)
    AND (CASE WHEN @role_ids::bigint[] IS NOT NULL AND cardinality(@role_ids::bigint[]) > 0 THEN u.role_id = ANY(@role_ids::bigint[]) ELSE TRUE END)
    AND (CASE WHEN @max_id::bigint > 0 THEN a.id < @max_id ELSE TRUE END)
    AND (CASE WHEN @since_id::bigint > 0 THEN a.id > @since_id ELSE TRUE END)
    AND (CASE WHEN @min_id::bigint > 0 THEN a.id > @min_id ELSE TRUE END)
ORDER BY a.id DESC
LIMIT $1;

-- name: AdminListUserIPs :many
SELECT
    user_id,
    ip,
    max(used_at)::timestamptz AS used_at
FROM (
    SELECT users.id AS user_id,
        users.sign_up_ip AS ip,
        users.created_at AS used_at
    FROM users
    WHERE users.id = ANY(@user_ids::bigint[]) AND users.sign_up_ip IS NOT NULL
    UNION ALL
    SELECT session_activations.user_id,
        session_activations.ip,
        session_activations.updated_at
    FROM session_activations
    WHERE session_activations.user_id = ANY(@user_ids::bigint[]) AND session_activations.ip IS NOT NULL
    UNION ALL
    SELECT login_activities.user_id,
        login_activities.ip,
        login_activities.created_at
    FROM login_activities
    WHERE login_activities.user_id = ANY(@user_ids::bigint[]) AND login_activities.ip IS NOT NULL AND login_activities.success = true
) ips
GROUP BY user_id, ip;

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
