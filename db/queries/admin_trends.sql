-- name: AdminListTrendingTags :many
SELECT
    t.id,
    t.name,
    t.created_at,
    t.updated_at,
    t.usable,
    t.trendable,
    t.listable,
    t.reviewed_at,
    t.requested_review_at,
    t.last_status_at,
    t.max_score,
    t.max_score_at,
    t.display_name,
    COUNT(st.status_id) AS usage_count,
    COUNT(DISTINCT s.account_id) AS account_count
FROM tags t
JOIN statuses_tags st ON t.id = st.tag_id
JOIN statuses s ON st.status_id = s.id
WHERE s.created_at > NOW() - INTERVAL '24 hours'
GROUP BY t.id
ORDER BY usage_count DESC
LIMIT $1;

-- name: AdminListTrendingStatuses :many
SELECT s.* FROM statuses s
JOIN status_stats ss ON s.id = ss.status_id
WHERE s.created_at > NOW() - INTERVAL '24 hours'
  AND s.visibility = 0
  AND s.deleted_at IS NULL
ORDER BY (ss.favourites_count + ss.reblogs_count) DESC
LIMIT $1;

-- name: AdminListTrendingLinks :many
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
    pc.author_account_id
FROM preview_cards pc
JOIN preview_card_trends pct ON pct.preview_card_id = pc.id
ORDER BY pct.rank ASC
LIMIT $1;

-- name: AdminUpdateTagTrendable :one
UPDATE tags
SET trendable = $2,
    reviewed_at = now(),
    updated_at = now()
WHERE id = $1
RETURNING id, name, created_at, updated_at, usable, trendable, listable, reviewed_at, requested_review_at, last_status_at, max_score, max_score_at, display_name;

-- name: AdminUpdateStatusTrendable :one
UPDATE statuses
SET trendable = $2,
    updated_at = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: AdminUpdatePreviewCardTrendable :one
UPDATE preview_cards
SET trendable = $2,
    updated_at = now()
WHERE id = $1
RETURNING id, url, title, description, image_file_name, image_content_type, image_file_size, image_updated_at, type, html, author_name, author_url, provider_name, provider_url, width, height, created_at, updated_at, embed_url, image_storage_schema_version, blurhash, language, max_score, max_score_at, trendable, link_type, published_at, image_description, author_account_id;
