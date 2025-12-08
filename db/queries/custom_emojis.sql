-- name: ListCustomEmojis :many
SELECT * FROM custom_emojis
WHERE disabled = false
ORDER BY shortcode ASC;

-- name: GetCustomEmoji :one
SELECT * FROM custom_emojis
WHERE shortcode = $1 AND (domain IS NULL OR domain = $2)
LIMIT 1;

-- name: CreateCustomEmoji :one
INSERT INTO custom_emojis (
    shortcode, domain, image_remote_url, category_id, visible_in_picker
) VALUES (
    $1, $2, $3, $4, $5
) RETURNING *;
