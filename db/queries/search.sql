-- name: SearchAccountsV2 :many
SELECT * FROM accounts
WHERE search_index @@ websearch_to_tsquery('english', sqlc.arg(query))
ORDER BY ts_rank(search_index, websearch_to_tsquery('english', sqlc.arg(query))) DESC
LIMIT $1 OFFSET $2;

-- name: SearchStatuses :many
SELECT 
    s.*,
    a.username as account_username,
    a.display_name as account_display_name,
    a.avatar_remote_url as account_avatar
FROM statuses s
JOIN accounts a ON s.account_id = a.id
WHERE s.search_index @@ websearch_to_tsquery('english', sqlc.arg(query))
  AND (s.visibility = 0 OR s.visibility = 1) -- Public or Unlisted
ORDER BY ts_rank(s.search_index, websearch_to_tsquery('english', sqlc.arg(query))) DESC
LIMIT $1 OFFSET $2;

-- name: SearchHashtags :many
SELECT * FROM tags
WHERE name ILIKE sqlc.arg(query) || '%'
ORDER BY length(name) ASC, name ASC
LIMIT $1 OFFSET $2;
