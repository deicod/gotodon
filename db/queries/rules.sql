-- Rules queries

-- name: GetRule :one
SELECT * FROM rules WHERE id = $1 AND deleted_at IS NULL;

-- name: ListRules :many
SELECT * FROM rules 
WHERE deleted_at IS NULL
ORDER BY priority ASC, id ASC;

-- name: CreateRule :one
INSERT INTO rules (priority, text, hint)
VALUES ($1, $2, $3)
RETURNING *;

-- name: UpdateRule :one
UPDATE rules SET
    priority = $2,
    text = $3,
    hint = $4,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteRule :exec
UPDATE rules SET deleted_at = now(), updated_at = now()
WHERE id = $1;

-- name: HardDeleteRule :exec
DELETE FROM rules WHERE id = $1;

-- Rule Translations queries

-- name: GetRuleTranslation :one
SELECT * FROM rule_translations 
WHERE rule_id = $1 AND language = $2;

-- name: ListRuleTranslations :many
SELECT * FROM rule_translations 
WHERE rule_id = $1
ORDER BY language ASC;

-- name: UpsertRuleTranslation :one
INSERT INTO rule_translations (rule_id, language, text, hint)
VALUES ($1, $2, $3, $4)
ON CONFLICT (rule_id, language) DO UPDATE SET
    text = EXCLUDED.text,
    hint = EXCLUDED.hint,
    updated_at = now()
RETURNING *;

-- name: DeleteRuleTranslation :exec
DELETE FROM rule_translations 
WHERE rule_id = $1 AND language = $2;

-- Domain Allows queries

-- name: GetDomainAllow :one
SELECT * FROM domain_allows WHERE id = $1;

-- name: GetDomainAllowByDomain :one
SELECT * FROM domain_allows WHERE domain = $1;

-- name: ListDomainAllows :many
SELECT * FROM domain_allows 
ORDER BY domain ASC
LIMIT $1 OFFSET $2;

-- name: CountDomainAllows :one
SELECT COUNT(*) FROM domain_allows;

-- name: CreateDomainAllow :one
INSERT INTO domain_allows (domain)
VALUES ($1)
RETURNING *;

-- name: DeleteDomainAllow :exec
DELETE FROM domain_allows WHERE id = $1;

-- name: DeleteDomainAllowByDomain :exec
DELETE FROM domain_allows WHERE domain = $1;

-- name: IsDomainAllowed :one
SELECT EXISTS(
    SELECT 1 FROM domain_allows WHERE domain = $1
) AS is_allowed;

-- Tombstones queries

-- name: GetTombstone :one
SELECT * FROM tombstones WHERE id = $1;

-- name: GetTombstoneByURI :one
SELECT * FROM tombstones WHERE uri = $1;

-- name: CreateTombstone :one
INSERT INTO tombstones (account_id, uri, by_moderator)
VALUES ($1, $2, $3)
RETURNING *;

-- name: DeleteTombstone :exec
DELETE FROM tombstones WHERE id = $1;

-- name: DeleteTombstoneByURI :exec
DELETE FROM tombstones WHERE uri = $1;

-- name: ListTombstonesByAccount :many
SELECT * FROM tombstones 
WHERE account_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: ExistsTombstone :one
SELECT EXISTS(
    SELECT 1 FROM tombstones WHERE uri = $1
) AS exists;
