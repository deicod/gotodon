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
SELECT
    pc.id,
    pc.url,
    pc.title,
    pc.description,
    pc.image_file_name,
    pc.image_content_type,
    pc.image_file_size,
    pc.image_updated_at,
    pc.type,
    pc.html,
    pc.author_name,
    pc.author_url,
    pc.provider_name,
    pc.provider_url,
    pc.width,
    pc.height,
    pc.created_at,
    pc.updated_at,
    pc.embed_url,
    pc.image_storage_schema_version,
    pc.blurhash,
    pc.language,
    pc.max_score,
    pc.max_score_at,
    pc.trendable,
    pc.link_type,
    pc.published_at,
    pc.image_description,
    pc.author_account_id,
    COUNT(pcs.status_id) FILTER (WHERE s.created_at > NOW() - INTERVAL '24 hours') AS usage_count,
    COUNT(DISTINCT s.account_id) FILTER (WHERE s.created_at > NOW() - INTERVAL '24 hours') AS account_count
FROM preview_cards pc
JOIN preview_card_trends pct ON pct.preview_card_id = pc.id
LEFT JOIN preview_cards_statuses pcs ON pcs.preview_card_id = pc.id
LEFT JOIN statuses s ON s.id = pcs.status_id
WHERE pct.allowed = true
GROUP BY pc.id, pct.rank
ORDER BY pct.rank ASC
LIMIT $1 OFFSET $2;
