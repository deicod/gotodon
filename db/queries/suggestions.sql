-- name: GetFollowSuggestions :many
SELECT a.*
FROM accounts a
LEFT JOIN follows f ON f.target_account_id = a.id AND f.account_id = $1
WHERE a.id != $1
  AND f.id IS NULL -- Not already followed
  AND a.locked = false
  AND a.discoverable = true
ORDER BY a.created_at DESC -- Suggest new users for now, ideally by popularity
LIMIT $2;
