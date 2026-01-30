-- name: GetAccountByID :one
SELECT * FROM accounts WHERE id = $1 AND suspended_at IS NULL;

-- name: GetAccountByIDAllowSuspended :one
SELECT * FROM accounts WHERE id = $1;

-- name: GetAccountByUsername :one
SELECT * FROM accounts 
WHERE lower(username) = lower(@username) AND domain IS NULL AND suspended_at IS NULL;

-- name: GetAccountByUsernameAndDomain :one
SELECT * FROM accounts 
WHERE lower(username) = lower(@username) 
  AND lower(domain) = lower(@domain) 
  AND suspended_at IS NULL;

-- name: GetAccountByURI :one
SELECT * FROM accounts WHERE uri = $1;

-- name: ListLocalAccounts :many
SELECT * FROM accounts 
WHERE domain IS NULL AND suspended_at IS NULL
ORDER BY id DESC
LIMIT $1 OFFSET $2;

-- name: CreateAccount :one
INSERT INTO accounts (
    username, domain, private_key, public_key, display_name, note, 
    uri, url, locked, discoverable, indexable, actor_type, protocol
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
) RETURNING *;

-- name: UpdateAccount :one
UPDATE accounts SET
    display_name = COALESCE(sqlc.narg('display_name'), display_name),
    note = COALESCE(sqlc.narg('note'), note),
    locked = COALESCE(sqlc.narg('locked'), locked),
    discoverable = COALESCE(sqlc.narg('discoverable'), discoverable),
    indexable = COALESCE(sqlc.narg('indexable'), indexable),
    hide_collections = COALESCE(sqlc.narg('hide_collections'), hide_collections),
    fields = COALESCE(sqlc.narg('fields'), fields),
    uri = COALESCE(sqlc.narg('uri'), uri),
    url = COALESCE(sqlc.narg('url'), url),
    private_key = COALESCE(sqlc.narg('private_key'), private_key),
    public_key = COALESCE(sqlc.narg('public_key'), public_key),
    actor_type = COALESCE(sqlc.narg('actor_type'), actor_type),
    inbox_url = COALESCE(sqlc.narg('inbox_url'), inbox_url),
    outbox_url = COALESCE(sqlc.narg('outbox_url'), outbox_url),
    shared_inbox_url = COALESCE(sqlc.narg('shared_inbox_url'), shared_inbox_url),
    followers_url = COALESCE(sqlc.narg('followers_url'), followers_url),
    following_url = COALESCE(sqlc.narg('following_url'), following_url),
    featured_collection_url = COALESCE(sqlc.narg('featured_collection_url'), featured_collection_url),
    avatar_remote_url = COALESCE(sqlc.narg('avatar_remote_url'), avatar_remote_url),
    header_remote_url = COALESCE(sqlc.narg('header_remote_url'), header_remote_url),
    avatar_file_name = COALESCE(sqlc.narg('avatar_file_name'), avatar_file_name),
    header_file_name = COALESCE(sqlc.narg('header_file_name'), header_file_name),
    last_webfingered_at = COALESCE(sqlc.narg('last_webfingered_at'), last_webfingered_at),
    attribution_domains = COALESCE(sqlc.narg('attribution_domains'), attribution_domains),
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: SuspendAccount :exec
UPDATE accounts SET suspended_at = now(), suspension_origin = $2, updated_at = now()
WHERE id = $1;

-- name: UnsuspendAccount :exec
UPDATE accounts SET suspended_at = NULL, suspension_origin = NULL, updated_at = now()
WHERE id = $1;

-- name: GetAccountStats :one
SELECT * FROM account_stats WHERE account_id = $1;

-- name: ListAccountStatsByAccountIDs :many
SELECT * FROM account_stats
WHERE account_id = ANY(@account_ids::bigint[]);

-- name: CreateAccountStats :one
INSERT INTO account_stats (account_id) VALUES ($1) RETURNING *;

-- name: UpdateAccountStats :exec
UPDATE account_stats SET
    statuses_count = statuses_count + sqlc.arg('statuses_delta'),
    following_count = following_count + sqlc.arg('following_delta'),
    followers_count = followers_count + sqlc.arg('followers_delta'),
    last_status_at = COALESCE(sqlc.narg('last_status_at'), last_status_at),
    updated_at = now()
WHERE account_id = $1;

-- name: SearchAccounts :many
SELECT * FROM accounts
WHERE suspended_at IS NULL
  AND (
    lower(username) LIKE lower(@query) || '%'
    OR lower(display_name) LIKE '%' || lower(@query) || '%'
  )
ORDER BY 
    CASE WHEN domain IS NULL THEN 0 ELSE 1 END,
    lower(username) = lower(@query) DESC,
    id DESC
LIMIT $1 OFFSET $2;

-- name: SearchAccountsFollowing :many
SELECT a.* FROM accounts a
LEFT JOIN follows f ON f.target_account_id = a.id AND f.account_id = $1
WHERE a.suspended_at IS NULL
  AND (f.account_id IS NOT NULL OR a.id = $1)
  AND (
    lower(a.username) LIKE lower(@query) || '%'
    OR lower(a.display_name) LIKE '%' || lower(@query) || '%'
  )
ORDER BY
    CASE WHEN a.domain IS NULL THEN 0 ELSE 1 END,
    lower(a.username) = lower(@query) DESC,
    a.id DESC
LIMIT $2 OFFSET $3;

-- name: GetAccountsByUsernames :many
SELECT * FROM accounts 
WHERE lower(username) = ANY(@usernames::text[]) 
  AND domain IS NULL 
  AND suspended_at IS NULL;

-- name: GetAccountsByUsernamesAndDomains :many
SELECT * FROM accounts
WHERE (lower(username), lower(domain)) IN (
    SELECT lower(unnest(@usernames::text[])), lower(unnest(@domains::text[]))
)
AND suspended_at IS NULL;

-- name: GetKnownDomains :many
SELECT DISTINCT domain FROM accounts 
WHERE domain IS NOT NULL AND domain != '' AND suspended_at IS NULL
ORDER BY domain;

-- name: ClearAccountAvatar :one
UPDATE accounts SET
    avatar_file_name = '',
    avatar_content_type = '',
    avatar_file_size = NULL,
    avatar_updated_at = NULL,
    avatar_remote_url = '',
    avatar_storage_schema_version = NULL,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: ClearAccountHeader :one
UPDATE accounts SET
    header_file_name = '',
    header_content_type = '',
    header_file_size = NULL,
    header_updated_at = NULL,
    header_remote_url = '',
    header_storage_schema_version = NULL,
    updated_at = now()
WHERE id = $1
RETURNING *;
