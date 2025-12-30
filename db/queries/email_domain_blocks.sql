-- name: AdminListEmailDomainBlocks :many
SELECT id, domain, created_at, allow_with_approval
FROM email_domain_blocks
ORDER BY id DESC
LIMIT $1 OFFSET $2;

-- name: AdminGetEmailDomainBlock :one
SELECT id, domain, created_at, allow_with_approval
FROM email_domain_blocks
WHERE id = $1;

-- name: AdminGetEmailDomainBlockByDomain :one
SELECT id, domain, created_at, allow_with_approval
FROM email_domain_blocks
WHERE domain = $1;

-- name: AdminCreateEmailDomainBlock :one
INSERT INTO email_domain_blocks (domain, allow_with_approval)
VALUES ($1, $2)
RETURNING id, domain, created_at, allow_with_approval;

-- name: AdminDeleteEmailDomainBlock :exec
DELETE FROM email_domain_blocks WHERE id = $1;
