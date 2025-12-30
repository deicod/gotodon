-- name: ListConversations :many
SELECT DISTINCT ON (s.conversation_id) 
    c.id, c.uri, c.created_at, c.updated_at,
    s.id as last_status_id,
    s.created_at as last_status_at,
    s.account_id as last_status_account_id
FROM conversations c
JOIN statuses s ON c.id = s.conversation_id
WHERE s.visibility = 3 -- Direct
  AND s.account_id = $1 OR s.id IN (
      SELECT status_id FROM mentions WHERE account_id = $1
  )
ORDER BY s.conversation_id, s.created_at DESC
LIMIT $2 OFFSET $3;

-- name: GetConversation :one
SELECT * FROM conversations WHERE id = $1;

-- name: CreateConversation :one
INSERT INTO conversations (uri, parent_status_id, parent_account_id)
VALUES ($1, $2, $3)
RETURNING *;

-- name: DeleteConversation :exec
DELETE FROM conversations WHERE id = $1;

-- name: CreateConversationMute :exec
INSERT INTO conversation_mutes (account_id, conversation_id)
VALUES ($1, $2)
ON CONFLICT DO NOTHING;

-- name: DeleteConversationMute :exec
DELETE FROM conversation_mutes
WHERE account_id = $1 AND conversation_id = $2;

-- name: ListConversationMutes :many
SELECT conversation_id
FROM conversation_mutes
WHERE account_id = $1
  AND conversation_id = ANY(sqlc.arg('conversation_ids')::bigint[]);

-- name: ListAccountConversations :many
SELECT *
FROM account_conversations
WHERE account_id = $1
ORDER BY last_status_id DESC NULLS LAST, id DESC
LIMIT $2 OFFSET $3;

-- name: GetAccountConversation :one
SELECT * FROM account_conversations
WHERE account_id = $1 AND id = $2;

-- name: DeleteAccountConversation :execrows
DELETE FROM account_conversations
WHERE account_id = $1 AND id = $2;

-- name: MarkConversationRead :execrows
UPDATE account_conversations
SET unread = false
WHERE account_id = $1 AND id = $2;

-- name: MarkConversationUnread :execrows
UPDATE account_conversations
SET unread = true
WHERE account_id = $1 AND id = $2;
