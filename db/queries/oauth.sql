-- name: GetOAuthApplication :one
SELECT * FROM oauth_applications WHERE id = $1;

-- name: GetOAuthApplicationByUID :one
SELECT * FROM oauth_applications WHERE uid = $1;

-- name: CreateOAuthApplication :one
INSERT INTO oauth_applications (
    name, uid, secret, redirect_uri, scopes, website, owner_type, owner_id, confidential
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9
) RETURNING *;

-- name: UpdateOAuthApplication :one
UPDATE oauth_applications SET
    name = COALESCE(sqlc.narg('name'), name),
    redirect_uri = COALESCE(sqlc.narg('redirect_uri'), redirect_uri),
    scopes = COALESCE(sqlc.narg('scopes'), scopes),
    website = COALESCE(sqlc.narg('website'), website),
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteOAuthApplication :exec
DELETE FROM oauth_applications WHERE id = $1;

-- name: ListOAuthApplicationsByOwner :many
SELECT * FROM oauth_applications 
WHERE owner_type = $1 AND owner_id = $2
ORDER BY created_at DESC;

-- name: GetOAuthAccessToken :one
SELECT * FROM oauth_access_tokens 
WHERE token = $1 AND revoked_at IS NULL;

-- name: GetOAuthAccessTokenByRefresh :one
SELECT * FROM oauth_access_tokens 
WHERE refresh_token = $1 AND revoked_at IS NULL;

-- name: CreateOAuthAccessToken :one
INSERT INTO oauth_access_tokens (
    token, refresh_token, expires_in, scopes, application_id, resource_owner_id
) VALUES (
    $1, $2, $3, $4, $5, $6
) RETURNING *;

-- name: RevokeOAuthAccessToken :exec
UPDATE oauth_access_tokens SET revoked_at = now() WHERE token = $1;

-- name: RevokeOAuthAccessTokensByApplication :exec
UPDATE oauth_access_tokens SET revoked_at = now() 
WHERE application_id = $1 AND resource_owner_id = $2 AND revoked_at IS NULL;

-- name: RevokeOAuthAccessTokensByUser :exec
UPDATE oauth_access_tokens SET revoked_at = now() 
WHERE resource_owner_id = $1 AND revoked_at IS NULL;

-- name: UpdateOAuthTokenLastUsed :exec
UPDATE oauth_access_tokens SET 
    last_used_at = now(), 
    last_used_ip = $2
WHERE id = $1;

-- name: ListUserOAuthTokens :many
SELECT t.*, a.name as app_name, a.website as app_website
FROM oauth_access_tokens t
JOIN oauth_applications a ON t.application_id = a.id
WHERE t.resource_owner_id = $1 AND t.revoked_at IS NULL
ORDER BY t.last_used_at DESC NULLS LAST;

-- name: CreateOAuthAccessGrant :one
INSERT INTO oauth_access_grants (
    token, expires_in, redirect_uri, scopes, application_id, resource_owner_id, code_challenge, code_challenge_method
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8
) RETURNING *;

-- name: GetOAuthAccessGrantByToken :one
SELECT * FROM oauth_access_grants
WHERE token = $1 AND revoked_at IS NULL;

-- name: RevokeOAuthAccessGrant :exec
UPDATE oauth_access_grants SET revoked_at = now() WHERE token = $1;
