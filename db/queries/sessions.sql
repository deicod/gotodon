-- Session Activations queries

-- name: GetSessionActivation :one
SELECT * FROM session_activations WHERE id = $1;

-- name: GetSessionActivationBySessionID :one
SELECT * FROM session_activations WHERE session_id = $1;

-- name: ListSessionActivationsByUser :many
SELECT * FROM session_activations 
WHERE user_id = $1
ORDER BY updated_at DESC;

-- name: CreateSessionActivation :one
INSERT INTO session_activations (
    session_id, user_agent, ip, access_token_id, user_id, web_push_subscription_id
) VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: UpdateSessionActivation :one
UPDATE session_activations SET
    user_agent = $2,
    ip = $3,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteSessionActivation :exec
DELETE FROM session_activations WHERE id = $1;

-- name: DeleteSessionActivationBySessionID :exec
DELETE FROM session_activations WHERE session_id = $1;

-- name: DeleteSessionActivationsByUser :exec
DELETE FROM session_activations WHERE user_id = $1;

-- name: CountSessionActivationsByUser :one
SELECT COUNT(*) FROM session_activations WHERE user_id = $1;

-- Login Activities queries

-- name: CreateLoginActivity :one
INSERT INTO login_activities (
    user_id, authentication_method, provider, success, failure_reason, ip, user_agent
) VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: ListLoginActivitiesByUser :many
SELECT * FROM login_activities 
WHERE user_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: CountFailedLoginAttempts :one
SELECT COUNT(*) FROM login_activities 
WHERE user_id = $1 
  AND success = false 
  AND created_at > $2;

-- name: GetLastSuccessfulLogin :one
SELECT * FROM login_activities 
WHERE user_id = $1 AND success = true
ORDER BY created_at DESC
LIMIT 1;

-- name: CountSuccessfulLoginActivitiesByMaskedIP :one
SELECT COUNT(*)
FROM login_activities
WHERE user_id = $1
  AND success = true
  AND ip IS NOT NULL
  AND ip <<= (@masked_ip::text)::inet;

-- WebAuthn Credentials queries

-- name: GetWebAuthnCredential :one
SELECT * FROM webauthn_credentials WHERE id = $1;

-- name: GetWebAuthnCredentialByExternalID :one
SELECT * FROM webauthn_credentials WHERE external_id = $1;

-- name: ListWebAuthnCredentialsByUser :many
SELECT * FROM webauthn_credentials 
WHERE user_id = $1
ORDER BY created_at ASC;

-- name: CreateWebAuthnCredential :one
INSERT INTO webauthn_credentials (
    external_id, public_key, nickname, sign_count, user_id
) VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: UpdateWebAuthnCredentialSignCount :one
UPDATE webauthn_credentials SET
    sign_count = $2,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateWebAuthnCredentialNickname :one
UPDATE webauthn_credentials SET
    nickname = $2,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteWebAuthnCredential :exec
DELETE FROM webauthn_credentials WHERE id = $1;

-- name: CountWebAuthnCredentialsByUser :one
SELECT COUNT(*) FROM webauthn_credentials WHERE user_id = $1;
