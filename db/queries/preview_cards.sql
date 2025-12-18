-- Preview Cards queries
-- name: GetPreviewCard :one
SELECT * FROM preview_cards WHERE id = $1;

-- name: GetPreviewCardByURL :one
SELECT * FROM preview_cards WHERE url = $1;

-- name: CreatePreviewCard :one
INSERT INTO preview_cards (
    url, title, description, image_file_name, image_content_type,
    image_file_size, image_updated_at, type, html, author_name,
    author_url, provider_name, provider_url, width, height,
    embed_url, blurhash, language, trendable, link_type,
    published_at, image_description, author_account_id
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
    $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23
) RETURNING *;

-- name: UpdatePreviewCard :one
UPDATE preview_cards SET
    title = $2,
    description = $3,
    image_file_name = $4,
    image_content_type = $5,
    image_file_size = $6,
    image_updated_at = $7,
    type = $8,
    html = $9,
    author_name = $10,
    author_url = $11,
    provider_name = $12,
    provider_url = $13,
    width = $14,
    height = $15,
    embed_url = $16,
    blurhash = $17,
    language = $18,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeletePreviewCard :exec
DELETE FROM preview_cards WHERE id = $1;

-- name: GetPreviewCardsForStatus :many
SELECT pc.* FROM preview_cards pc
JOIN preview_cards_statuses pcs ON pcs.preview_card_id = pc.id
WHERE pcs.status_id = $1;

-- name: AttachPreviewCardToStatus :exec
INSERT INTO preview_cards_statuses (preview_card_id, status_id, url)
VALUES ($1, $2, $3)
ON CONFLICT (status_id, preview_card_id) DO NOTHING;

-- name: DetachPreviewCardFromStatus :exec
DELETE FROM preview_cards_statuses 
WHERE preview_card_id = $1 AND status_id = $2;

-- name: GetTrendingPreviewCards :many
SELECT pc.*, pct.score, pct.rank FROM preview_cards pc
JOIN preview_card_trends pct ON pct.preview_card_id = pc.id
WHERE pct.allowed = true
ORDER BY pct.rank ASC
LIMIT $1 OFFSET $2;

-- Preview Card Providers
-- name: GetPreviewCardProvider :one
SELECT * FROM preview_card_providers WHERE id = $1;

-- name: GetPreviewCardProviderByDomain :one
SELECT * FROM preview_card_providers WHERE domain = $1;

-- name: CreatePreviewCardProvider :one
INSERT INTO preview_card_providers (domain, trendable)
VALUES ($1, $2)
RETURNING *;

-- name: UpdatePreviewCardProviderTrendable :one
UPDATE preview_card_providers SET 
    trendable = $2,
    reviewed_at = now(),
    updated_at = now()
WHERE id = $1
RETURNING *;

-- Preview Card Trends
-- name: UpsertPreviewCardTrend :one
INSERT INTO preview_card_trends (preview_card_id, score, rank, allowed, language)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (preview_card_id) DO UPDATE SET
    score = EXCLUDED.score,
    rank = EXCLUDED.rank,
    allowed = EXCLUDED.allowed,
    language = EXCLUDED.language
RETURNING *;

-- name: GetPreviewCardTrend :one
SELECT * FROM preview_card_trends WHERE preview_card_id = $1;
