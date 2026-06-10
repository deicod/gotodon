-- name: CreateUser :one
INSERT INTO users (
    email, encrypted_password, account_id, confirmed_at, invite_id, role_id,
    confirmation_token, confirmation_sent_at, locale, approved
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10
) RETURNING *;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE LOWER(email) = LOWER($1);

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: GetUserByConfirmationToken :one
SELECT * FROM users WHERE confirmation_token = $1;

-- name: GetUserByResetPasswordToken :one
SELECT * FROM users WHERE reset_password_token = $1 AND reset_password_token IS NOT NULL;

-- name: GetUserByAccountID :one
SELECT * FROM users WHERE account_id = $1;

-- name: UpdateUserConfirmationToken :exec
UPDATE users
SET confirmation_token = $2,
    confirmation_sent_at = now(),
    updated_at = now()
WHERE id = $1;

-- name: ListUserSettingsByAccountIDs :many
SELECT account_id, settings
FROM users
WHERE account_id = ANY(@account_ids::bigint[]);

-- name: UpdateUserSettings :exec
UPDATE users SET
    settings = COALESCE($2, settings),
    updated_at = now()
WHERE account_id = $1;

-- name: ListUserRolesByAccountIDs :many
SELECT u.account_id, r.id, r.name, r.color, r.permissions, r.highlighted
FROM users u
JOIN user_roles r ON r.id = u.role_id
WHERE u.account_id = ANY(@account_ids::bigint[]);

-- name: CountUserRegistrationsInRange :one
SELECT COUNT(*)
FROM users
WHERE created_at >= $1 AND created_at < $2;

-- name: CountUserLoginsInRange :one
SELECT COUNT(*)
FROM users
WHERE COALESCE(current_sign_in_at, last_sign_in_at) >= $1
  AND COALESCE(current_sign_in_at, last_sign_in_at) < $2;

-- name: GetUserRole :one
SELECT r.* FROM user_roles r
JOIN users u ON u.role_id = r.id
WHERE u.id = $1;

-- name: ConfirmUser :exec
UPDATE users
SET confirmed_at = now(),
    confirmation_token = NULL,
    updated_at = now()
WHERE id = $1;

-- name: UpdateUserEmail :exec
UPDATE users
SET email = $2,
    updated_at = now()
WHERE id = $1;

-- name: UpdateUserResetPasswordToken :exec
UPDATE users
SET reset_password_token = $2,
    reset_password_sent_at = now(),
    updated_at = now()
WHERE id = $1;

-- name: UpdateUserPassword :exec
UPDATE users
SET encrypted_password = $2,
    reset_password_token = NULL,
    reset_password_sent_at = NULL,
    updated_at = now()
WHERE id = $1;

-- name: UpdateUserSignIn :exec
UPDATE users
SET sign_in_count = sign_in_count + 1,
    last_sign_in_at = current_sign_in_at,
    current_sign_in_at = now(),
    updated_at = now()
WHERE id = $1;

-- name: ListUsersWithPermission :many
-- The administrator flag (bit 0) implies every permission, mirroring Mastodon's UserRole.
SELECT u.* FROM users u
JOIN user_roles r ON r.id = u.role_id
WHERE ((r.permissions & @permission::bigint) != 0 OR (r.permissions & 1) != 0)
  AND u.disabled = false
  AND u.approved = true
  AND u.confirmed_at IS NOT NULL;
