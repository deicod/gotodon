-- name: GetPreferences :one
SELECT * FROM preferences WHERE account_id = $1;

-- name: UpsertPreferences :one
INSERT INTO preferences (
    account_id, 
    posting_default_visibility, 
    posting_default_sensitive, 
    posting_default_language, 
    reading_expand_media, 
    reading_expand_spoilers
) VALUES (
    $1, $2, $3, $4, $5, $6
)
ON CONFLICT (account_id) DO UPDATE SET
    posting_default_visibility = EXCLUDED.posting_default_visibility,
    posting_default_sensitive = EXCLUDED.posting_default_sensitive,
    posting_default_language = EXCLUDED.posting_default_language,
    reading_expand_media = EXCLUDED.reading_expand_media,
    reading_expand_spoilers = EXCLUDED.reading_expand_spoilers,
    updated_at = now()
RETURNING *;
