-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE lower(email) = lower($1);

-- name: GetUserByAccountID :one
SELECT * FROM users WHERE account_id = $1;

-- name: GetUserByConfirmationToken :one
SELECT * FROM users WHERE confirmation_token = $1;

-- name: GetUserByResetPasswordToken :one
SELECT * FROM users WHERE reset_password_token = $1;

-- name: CreateUser :one
INSERT INTO users (
    email, encrypted_password, account_id, locale, 
    confirmation_token, confirmation_sent_at, approved, role_id
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8
) RETURNING *;

-- name: UpdateUser :one
UPDATE users SET
    email = COALESCE(sqlc.narg('email'), email),
    encrypted_password = COALESCE(sqlc.narg('encrypted_password'), encrypted_password),
    locale = COALESCE(sqlc.narg('locale'), locale),
    chosen_languages = COALESCE(sqlc.narg('chosen_languages'), chosen_languages),
    confirmed_at = COALESCE(sqlc.narg('confirmed_at'), confirmed_at),
    current_sign_in_at = COALESCE(sqlc.narg('current_sign_in_at'), current_sign_in_at),
    last_sign_in_at = COALESCE(sqlc.narg('last_sign_in_at'), last_sign_in_at),
    sign_in_count = sign_in_count + COALESCE(sqlc.narg('sign_in_count_delta'), 0),
    otp_required_for_login = COALESCE(sqlc.narg('otp_required_for_login'), otp_required_for_login),
    disabled = COALESCE(sqlc.narg('disabled'), disabled),
    approved = COALESCE(sqlc.narg('approved'), approved),
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: ConfirmUser :exec
UPDATE users SET 
    confirmed_at = now(), 
    confirmation_token = NULL,
    updated_at = now()
WHERE id = $1;

-- name: SetResetPasswordToken :exec
UPDATE users SET 
    reset_password_token = $2, 
    reset_password_sent_at = now(),
    updated_at = now()
WHERE id = $1;

-- name: ClearResetPasswordToken :exec
UPDATE users SET 
    reset_password_token = NULL, 
    reset_password_sent_at = NULL,
    updated_at = now()
WHERE id = $1;

-- name: GetUserRole :one
SELECT r.* FROM user_roles r
JOIN users u ON u.role_id = r.id
WHERE u.id = $1;

-- name: ListUserRoles :many
SELECT * FROM user_roles ORDER BY position DESC;
