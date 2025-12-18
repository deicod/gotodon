-- name: GetAppByClientID :one
SELECT id, name, website, redirect_uri, scopes, confidential 
FROM oauth_applications 
WHERE uid = $1 LIMIT 1;

-- name: GetAccountsByIDs :many
SELECT * FROM accounts WHERE id = ANY(@ids::bigint[]);

-- name: GetStatusStatsByIDs :many
SELECT * FROM status_stats WHERE status_id = ANY(@status_ids::bigint[]);

-- name: GetMediaAttachmentsByStatusIDs :many
SELECT * FROM media_attachments WHERE status_id = ANY(@status_ids::bigint[]);

-- name: GetMentionsByStatusIDs :many
SELECT * FROM mentions WHERE status_id = ANY(@status_ids::bigint[]);

-- name: GetTagsByStatusIDs :many
SELECT st.status_id, t.id, t.name
FROM statuses_tags st
JOIN tags t ON st.tag_id = t.id
WHERE st.status_id = ANY(@status_ids::bigint[]);

