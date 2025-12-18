-- Account Notes queries (private notes about other users)

-- name: GetAccountNote :one
SELECT * FROM account_notes 
WHERE account_id = $1 AND target_account_id = $2;

-- name: CreateAccountNote :one
INSERT INTO account_notes (account_id, target_account_id, comment)
VALUES ($1, $2, $3)
RETURNING *;

-- name: UpdateAccountNote :one
UPDATE account_notes SET
    comment = $3,
    updated_at = now()
WHERE account_id = $1 AND target_account_id = $2
RETURNING *;

-- name: UpsertAccountNote :one
INSERT INTO account_notes (account_id, target_account_id, comment)
VALUES ($1, $2, $3)
ON CONFLICT (account_id, target_account_id) DO UPDATE SET
    comment = EXCLUDED.comment,
    updated_at = now()
RETURNING *;

-- name: DeleteAccountNote :exec
DELETE FROM account_notes 
WHERE account_id = $1 AND target_account_id = $2;

-- Account Aliases queries (for account migration)

-- name: GetAccountAliases :many
SELECT * FROM account_aliases 
WHERE account_id = $1
ORDER BY created_at DESC;

-- name: CreateAccountAlias :one
INSERT INTO account_aliases (account_id, acct, uri)
VALUES ($1, $2, $3)
RETURNING *;

-- name: DeleteAccountAlias :exec
DELETE FROM account_aliases WHERE id = $1;

-- name: DeleteAccountAliasesByAccountID :exec
DELETE FROM account_aliases WHERE account_id = $1;

-- name: GetAccountAliasByURI :one
SELECT * FROM account_aliases 
WHERE account_id = $1 AND uri = $2;

-- Account Migrations queries

-- name: GetAccountMigration :one
SELECT * FROM account_migrations WHERE id = $1;

-- name: GetAccountMigrationsByAccountID :many
SELECT * FROM account_migrations 
WHERE account_id = $1
ORDER BY created_at DESC;

-- name: GetLatestAccountMigration :one
SELECT * FROM account_migrations 
WHERE account_id = $1
ORDER BY created_at DESC
LIMIT 1;

-- name: CreateAccountMigration :one
INSERT INTO account_migrations (account_id, acct, followers_count, target_account_id)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetMigrationsByTargetAccount :many
SELECT * FROM account_migrations 
WHERE target_account_id = $1
ORDER BY created_at DESC;

-- name: CountMigrationFollowers :one
SELECT COALESCE(SUM(followers_count), 0)::bigint AS total_followers 
FROM account_migrations 
WHERE target_account_id = $1;
