-- name: GetTrendingTags :many
SELECT 
    t.id, t.name, t.created_at,
    COUNT(st.status_id) as usage_count,
    COUNT(DISTINCT s.account_id) as account_count
FROM tags t
JOIN statuses_tags st ON t.id = st.tag_id
JOIN statuses s ON st.status_id = s.id
WHERE s.created_at > NOW() - INTERVAL '24 hours'
GROUP BY t.id, t.name
ORDER BY usage_count DESC
LIMIT $1;

-- name: GetTrendingStatuses :many
SELECT 
    s.*,
    ss.favourites_count,
    ss.reblogs_count,
    ss.replies_count,
    a.username as account_username,
    a.display_name as account_display_name,
    a.avatar_remote_url as account_avatar
FROM statuses s
JOIN status_stats ss ON s.id = ss.status_id
JOIN accounts a ON s.account_id = a.id
WHERE s.created_at > NOW() - INTERVAL '24 hours'
  AND s.visibility = 0 -- Public only
ORDER BY (ss.favourites_count + ss.reblogs_count) DESC
LIMIT $1;

-- name: GetTrendingLinks :many
-- Placeholder: In future, use preview_cards table
SELECT 1;
