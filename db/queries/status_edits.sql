-- Status Edits queries
-- name: GetStatusEditsByStatusID :many
SELECT * FROM status_edits 
WHERE status_id = $1 
ORDER BY created_at ASC;

-- name: CreateStatusEdit :one
INSERT INTO status_edits (
    status_id, account_id, text, spoiler_text, 
    ordered_media_attachment_ids, media_descriptions, 
    poll_options, sensitive, quote_id
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9
) RETURNING *;

-- name: CountStatusEdits :one
SELECT COUNT(*) FROM status_edits WHERE status_id = $1;

-- name: GetStatusEdit :one
SELECT * FROM status_edits WHERE id = $1;

-- name: DeleteStatusEditsForStatus :exec
DELETE FROM status_edits WHERE status_id = $1;
