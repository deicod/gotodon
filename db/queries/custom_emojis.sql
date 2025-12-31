-- name: ListCustomEmojis :many
SELECT * FROM custom_emojis
WHERE disabled = false
ORDER BY shortcode ASC;

-- name: ListCustomEmojisByIDs :many
SELECT * FROM custom_emojis
WHERE disabled = false
  AND id = ANY(@ids::bigint[]);

-- name: ListCustomEmojisByShortcodesAndDomain :many
SELECT * FROM custom_emojis
WHERE disabled = false
  AND shortcode = ANY(@shortcodes::text[])
  AND (
    (domain IS NULL AND @domain = '')
    OR domain = @domain
  );

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

-- name: AdminListCustomEmojis :many
SELECT * FROM custom_emojis
ORDER BY id DESC
LIMIT $1 OFFSET $2;

-- name: AdminCreateCustomEmoji :one
INSERT INTO custom_emojis (
    shortcode, domain, image_file_name, image_content_type, image_file_size, image_updated_at, category_id, visible_in_picker, created_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5, NOW(), $6, $7, NOW(), NOW()
) RETURNING *;

-- name: DeleteCustomEmoji :exec
DELETE FROM custom_emojis WHERE id = $1;

-- name: UpdateCustomEmoji :one
UPDATE custom_emojis
SET disabled = $2, visible_in_picker = $3, updated_at = NOW()
WHERE id = $1
RETURNING *;
