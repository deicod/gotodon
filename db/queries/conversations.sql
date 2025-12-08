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

-- name: MarkConversationRead :exec
-- Placeholder: Requires conversation_mutes or new table for read state which implies 'unread' logic
-- Currently just creating a stub query if we add unread tracking later
SELECT 1;
