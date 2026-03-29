-- name: CreateFollow :one
INSERT INTO follows (account_id, target_account_id, show_reblogs, notify, uri)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetFollow :one
SELECT * FROM follows 
WHERE account_id = $1 AND target_account_id = $2;

-- name: DeleteFollow :exec
DELETE FROM follows WHERE account_id = $1 AND target_account_id = $2;

-- name: GetFollowers :many
SELECT a.* FROM accounts a
JOIN follows f ON f.account_id = a.id
WHERE f.target_account_id = $1 AND a.suspended_at IS NULL
ORDER BY f.id DESC
LIMIT $2 OFFSET $3;

-- name: ListFollowerURIsByPrefix :many
SELECT a.uri FROM accounts a
JOIN follows f ON f.account_id = a.id
WHERE f.target_account_id = $1
  AND a.suspended_at IS NULL
  AND a.uri IS NOT NULL
  AND (sqlc.narg('prefix')::text IS NULL OR a.uri LIKE sqlc.narg('prefix')::text || '%')
ORDER BY f.id DESC;

-- name: GetFollowing :many
SELECT a.* FROM accounts a
JOIN follows f ON f.target_account_id = a.id
WHERE f.account_id = $1 AND a.suspended_at IS NULL
ORDER BY f.id DESC
LIMIT $2 OFFSET $3;

-- name: CountFollowers :one
SELECT COUNT(*) FROM follows WHERE target_account_id = $1;

-- name: CountFollowing :one
SELECT COUNT(*) FROM follows WHERE account_id = $1;

-- name: CreateFollowRequest :one
INSERT INTO follow_requests (account_id, target_account_id, show_reblogs, notify, uri)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetFollowRequest :one
SELECT * FROM follow_requests 
WHERE account_id = $1 AND target_account_id = $2;

-- name: ListFollowRequests :many
SELECT a.* FROM accounts a
JOIN follow_requests fr ON fr.account_id = a.id
WHERE fr.target_account_id = $1 AND a.suspended_at IS NULL
ORDER BY fr.id DESC
LIMIT $2 OFFSET $3;

-- name: CountFollowRequestsForAccount :one
SELECT COUNT(*) FROM follow_requests WHERE target_account_id = $1;

-- name: DeleteFollowRequest :exec
DELETE FROM follow_requests WHERE account_id = $1 AND target_account_id = $2;

-- name: CreateFavourite :one
INSERT INTO favourites (account_id, status_id)
VALUES ($1, $2)
RETURNING *;

-- name: ListFavouritedStatusIDs :many
SELECT status_id FROM favourites
WHERE account_id = $1
  AND status_id = ANY(@status_ids::bigint[]);

-- name: GetFavourite :one
SELECT * FROM favourites WHERE account_id = $1 AND status_id = $2;

-- name: DeleteFavourite :exec
DELETE FROM favourites WHERE account_id = $1 AND status_id = $2;

-- name: GetFavouritedBy :many
SELECT a.* FROM accounts a
JOIN favourites f ON f.account_id = a.id
WHERE f.status_id = $1 AND a.suspended_at IS NULL
ORDER BY f.id DESC
LIMIT $2 OFFSET $3;

-- name: ListFavourites :many
SELECT s.* FROM statuses s
JOIN favourites f ON f.status_id = s.id
WHERE f.account_id = $1 AND s.deleted_at IS NULL
ORDER BY f.id DESC
LIMIT $2 OFFSET $3;

-- name: CreateBlock :one
INSERT INTO blocks (account_id, target_account_id, uri)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetBlock :one
SELECT * FROM blocks WHERE account_id = $1 AND target_account_id = $2;

-- name: DeleteBlock :exec
DELETE FROM blocks WHERE account_id = $1 AND target_account_id = $2;

-- name: ListBlocks :many
SELECT a.* FROM accounts a
JOIN blocks b ON b.target_account_id = a.id
WHERE b.account_id = $1
ORDER BY b.id DESC
LIMIT $2 OFFSET $3;

-- name: CreateMute :one
INSERT INTO mutes (account_id, target_account_id, hide_notifications, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetMute :one
SELECT * FROM mutes WHERE account_id = $1 AND target_account_id = $2;

-- name: DeleteMute :exec
DELETE FROM mutes WHERE account_id = $1 AND target_account_id = $2;

-- name: ListMutes :many
SELECT a.* FROM accounts a
JOIN mutes m ON m.target_account_id = a.id
WHERE m.account_id = $1 AND (m.expires_at IS NULL OR m.expires_at > now())
ORDER BY m.id DESC
LIMIT $2 OFFSET $3;

-- name: GetRelationship :one
SELECT 
    f.id IS NOT NULL as following,
    fr.id IS NOT NULL as requested,
    fb.id IS NOT NULL as followed_by,
    b.id IS NOT NULL as blocking,
    bb.id IS NOT NULL as blocked_by,
    m.id IS NOT NULL as muting,
    COALESCE(m.hide_notifications, false) as muting_notifications,
    COALESCE(f.show_reblogs, true) as showing_reblogs,
    COALESCE(f.notify, false) as notifying,
    f.languages as languages,
    false as endorsed,
    db.id IS NOT NULL as domain_blocking,
    '' as note
FROM accounts a
LEFT JOIN follows f ON f.account_id = @account_id AND f.target_account_id = a.id
LEFT JOIN follow_requests fr ON fr.account_id = @account_id AND fr.target_account_id = a.id
LEFT JOIN follows fb ON fb.account_id = a.id AND fb.target_account_id = @account_id
LEFT JOIN blocks b ON b.account_id = @account_id AND b.target_account_id = a.id
LEFT JOIN blocks bb ON bb.account_id = a.id AND bb.target_account_id = @account_id
LEFT JOIN mutes m ON m.account_id = @account_id AND m.target_account_id = a.id
LEFT JOIN account_domain_blocks db ON db.account_id = @account_id AND db.domain = a.domain
WHERE a.id = @target_account_id;

-- name: ListDomainBlocks :many
SELECT * FROM account_domain_blocks
WHERE account_id = $1
ORDER BY id DESC
LIMIT $2 OFFSET $3;

-- name: CreateDomainBlock :one
INSERT INTO account_domain_blocks (account_id, domain)
VALUES ($1, $2)
RETURNING *;

-- name: DeleteDomainBlock :exec
DELETE FROM account_domain_blocks WHERE account_id = $1 AND domain = $2;

-- name: GetDomainBlock :one
SELECT * FROM account_domain_blocks WHERE account_id = $1 AND domain = $2;

-- name: ListDomainBlockDomains :many
SELECT domain FROM account_domain_blocks
WHERE account_id = $1;

-- name: GetDomainBlocksByAccountsAndDomain :many
SELECT account_id FROM account_domain_blocks
WHERE account_id = ANY(@account_ids::bigint[])
  AND domain = @domain;

-- name: GetFollowsByAccountIDs :many
SELECT target_account_id FROM follows
WHERE account_id = $1
  AND target_account_id = ANY(@target_account_ids::bigint[]);

-- name: GetFamiliarFollowers :many
SELECT a.* FROM accounts a
JOIN follows requested_followers ON requested_followers.account_id = a.id
JOIN follows requester_follows ON requester_follows.account_id = @account_id
  AND requester_follows.target_account_id = a.id
WHERE requested_followers.target_account_id = @target_account_id
  AND a.suspended_at IS NULL
  AND NOT (a.hide_collections IS TRUE)
ORDER BY requested_followers.id DESC;

-- name: CountFollowingByDomain :one
SELECT COUNT(*)
FROM follows f
JOIN accounts a ON a.id = f.target_account_id
WHERE f.account_id = $1
  AND a.domain = $2;

-- name: CountFollowersByDomain :one
SELECT COUNT(*)
FROM follows f
JOIN accounts a ON a.id = f.account_id
WHERE f.target_account_id = $1
  AND a.domain = $2;

-- name: GetRebloggers :many
SELECT a.* FROM accounts a
JOIN statuses s ON s.account_id = a.id AND s.reblog_of_id = $1
WHERE a.suspended_at IS NULL AND s.deleted_at IS NULL
ORDER BY s.id DESC
LIMIT $2 OFFSET $3;

-- name: GetMutesByAccountIDs :many
SELECT * FROM mutes 
WHERE account_id = $1 
AND target_account_id = ANY(@target_account_ids::bigint[]);

-- name: GetBlocksByAccountIDs :many
SELECT * FROM blocks
WHERE account_id = $1
AND target_account_id = ANY(@target_account_ids::bigint[]);

-- name: GetMutesFromAccounts :many
SELECT * FROM mutes
WHERE target_account_id = $1
AND account_id = ANY(@account_ids::bigint[]);

-- name: GetBlocksFromAccounts :many
SELECT * FROM blocks
WHERE target_account_id = $1
AND account_id = ANY(@account_ids::bigint[]);
