-- name: CreateUser :one
INSERT INTO users (
    email, encrypted_password, account_id, confirmed_at, invite_id, role_id
) VALUES (
    $1, $2, $3, $4, $5, $6
) RETURNING *;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE LOWER(email) = LOWER($1);

-- name: GetUserByConfirmationToken :one
SELECT * FROM users WHERE confirmation_token = $1;

-- name: GetUserByResetPasswordToken :one
SELECT * FROM users WHERE reset_password_token = $1 AND reset_password_token IS NOT NULL;

-- name: GetUserByAccountID :one
SELECT * FROM users WHERE account_id = $1;

-- name: UpdateUserSettings :exec
UPDATE users SET
    settings = COALESCE($2, settings),
    updated_at = now()
WHERE account_id = $1;

-- name: GetUserRole :one
SELECT r.* FROM user_roles r
JOIN users u ON u.role_id = r.id
WHERE u.id = $1;
