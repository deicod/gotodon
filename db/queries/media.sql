-- name: CreateMediaAttachment :one
INSERT INTO media_attachments (
    account_id, status_id, file_file_name, file_content_type, file_file_size,
    type, description, blurhash, processing
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9
) RETURNING *;

-- name: GetMediaAttachment :one
SELECT * FROM media_attachments WHERE id = $1;

-- name: GetMediaAttachmentByAccount :one
SELECT * FROM media_attachments WHERE id = $1 AND account_id = $2;

-- name: ListMediaAttachmentsByStatus :many
SELECT * FROM media_attachments WHERE status_id = $1 ORDER BY id;

-- name: ListUnattachedMedia :many
SELECT * FROM media_attachments 
WHERE account_id = $1 AND status_id IS NULL
ORDER BY id DESC
LIMIT $2;

-- name: UpdateMediaAttachment :one
UPDATE media_attachments SET
    description = COALESCE($2, description),
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: AttachMediaToStatus :exec
UPDATE media_attachments SET status_id = $2, updated_at = now()
WHERE id = $1 AND account_id = $3 AND status_id IS NULL;

-- name: DeleteMediaAttachment :exec
DELETE FROM media_attachments WHERE id = $1 AND account_id = $2;
