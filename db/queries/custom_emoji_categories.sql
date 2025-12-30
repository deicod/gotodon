-- name: GetCustomEmojiCategoriesByIDs :many
SELECT id, name
FROM custom_emoji_categories
WHERE id = ANY($1::bigint[]);
