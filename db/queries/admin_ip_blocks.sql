-- name: AdminListIpBlocks :many
SELECT id, ip::text AS ip, severity, comment, expires_at, created_at, updated_at
FROM ip_blocks
ORDER BY id DESC
LIMIT $1 OFFSET $2;

-- name: AdminGetIpBlock :one
SELECT id, ip::text AS ip, severity, comment, expires_at, created_at, updated_at
FROM ip_blocks
WHERE id = $1;

-- name: AdminGetIpBlockByIP :one
SELECT id, ip::text AS ip, severity, comment, expires_at, created_at, updated_at
FROM ip_blocks
WHERE ip::text = sqlc.arg('ip')::text;

-- name: AdminCreateIpBlock :one
INSERT INTO ip_blocks (ip, severity, expires_at, comment)
VALUES ($1::text, $2, $3, $4)
RETURNING id, ip::text AS ip, severity, comment, expires_at, created_at, updated_at;

-- name: AdminUpdateIpBlock :one
UPDATE ip_blocks
SET ip = $2::text,
    severity = $3,
    expires_at = $4,
    comment = $5,
    updated_at = now()
WHERE id = $1
RETURNING id, ip::text AS ip, severity, comment, expires_at, created_at, updated_at;

-- name: AdminDeleteIpBlock :exec
DELETE FROM ip_blocks WHERE id = $1;
