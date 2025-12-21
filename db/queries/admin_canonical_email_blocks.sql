-- name: AdminListCanonicalEmailBlocks :many
SELECT id, canonical_email_hash, reference_account_id, created_at, updated_at
FROM canonical_email_blocks
ORDER BY id DESC
LIMIT $1 OFFSET $2;

-- name: AdminGetCanonicalEmailBlock :one
SELECT id, canonical_email_hash, reference_account_id, created_at, updated_at
FROM canonical_email_blocks
WHERE id = $1;

-- name: AdminGetCanonicalEmailBlockByHash :one
SELECT id, canonical_email_hash, reference_account_id, created_at, updated_at
FROM canonical_email_blocks
WHERE canonical_email_hash = $1;

-- name: AdminCreateCanonicalEmailBlock :one
INSERT INTO canonical_email_blocks (
    canonical_email_hash, reference_account_id, created_at, updated_at
) VALUES (
    $1, $2, now(), now()
)
RETURNING id, canonical_email_hash, reference_account_id, created_at, updated_at;

-- name: AdminDeleteCanonicalEmailBlock :exec
DELETE FROM canonical_email_blocks WHERE id = $1;
