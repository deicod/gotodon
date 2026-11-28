-- name: AdminListDomainBlocks :many
SELECT * FROM domain_blocks
ORDER BY domain ASC
LIMIT $1 OFFSET $2;

-- name: ListPublicDomainBlocks :many
SELECT * FROM domain_blocks
WHERE severity IN (0, 1)
ORDER BY severity ASC, domain ASC;

-- name: AdminGetDomainBlock :one
SELECT * FROM domain_blocks WHERE id = $1;

-- name: AdminCreateDomainBlock :one
INSERT INTO domain_blocks (
    domain, severity, reject_media, reject_reports, private_comment, public_comment, obfuscate, created_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, now(), now()
)
RETURNING *;

-- name: AdminUpdateDomainBlock :one
UPDATE domain_blocks
SET 
    severity = COALESCE($2, severity),
    reject_media = COALESCE($3, reject_media),
    reject_reports = COALESCE($4, reject_reports),
    private_comment = COALESCE($5, private_comment),
    public_comment = COALESCE($6, public_comment),
    obfuscate = COALESCE($7, obfuscate),
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: AdminDeleteDomainBlock :exec
DELETE FROM domain_blocks WHERE id = $1;

-- name: DomainRejectsReports :one
-- DomainRejectsReports reports whether the most specific domain block among
-- domains, a domain followed by its parent domains, rejects reports, as
-- Mastodon's DomainBlock.reject_reports? decides. It is false when no block
-- covers the domain.
SELECT COALESCE((
    SELECT b.reject_reports FROM domain_blocks b
    WHERE lower(b.domain) = ANY(@domains::text[])
    ORDER BY length(b.domain) DESC
    LIMIT 1
), false)::boolean AS rejects_reports;
