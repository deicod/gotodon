-- name: CreateMediaAttachment :one
INSERT INTO media_attachments (
    account_id, status_id, file_file_name, file_content_type, file_file_size,
    type, description, blurhash, processing, shortcode, file_meta
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
) RETURNING *;

-- name: CreateRemoteMediaAttachment :one
INSERT INTO media_attachments (
    account_id, status_id, remote_url, thumbnail_remote_url, type, description,
    blurhash, file_content_type, file_meta, processing
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10
) RETURNING *;

-- name: GetMediaAttachment :one
SELECT * FROM media_attachments WHERE id = $1;

-- name: GetMediaAttachmentsByIDs :many
SELECT * FROM media_attachments WHERE id = ANY($1::bigint[]);

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
    description = COALESCE(sqlc.narg('description'), description),
    file_meta = COALESCE(sqlc.narg('file_meta'), file_meta),
    thumbnail_file_name = COALESCE(sqlc.narg('thumbnail_file_name'), thumbnail_file_name),
    thumbnail_content_type = COALESCE(sqlc.narg('thumbnail_content_type'), thumbnail_content_type),
    thumbnail_file_size = COALESCE(sqlc.narg('thumbnail_file_size'), thumbnail_file_size),
    updated_at = now()
WHERE id = $1 AND account_id = $2
RETURNING *;

-- name: UpdateMediaProcessing :one
UPDATE media_attachments SET
    file_meta = COALESCE($2, file_meta),
    blurhash = COALESCE($3, blurhash),
    processing = COALESCE($4, processing),
    thumbnail_file_name = COALESCE($5, thumbnail_file_name),
    thumbnail_content_type = COALESCE($6, thumbnail_content_type),
    thumbnail_file_size = COALESCE($7, thumbnail_file_size),
    thumbnail_updated_at = CASE
        WHEN $5 IS NULL AND $6 IS NULL AND $7 IS NULL THEN thumbnail_updated_at
        ELSE now()
    END,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: AttachMediaToStatus :execrows
UPDATE media_attachments SET status_id = $2, updated_at = now()
WHERE id = $1 AND account_id = $3 AND status_id IS NULL;

-- name: DeleteMediaAttachment :exec
DELETE FROM media_attachments WHERE id = $1 AND account_id = $2;

-- name: DetachMediaFromStatus :execrows
UPDATE media_attachments SET status_id = NULL, updated_at = now()
WHERE id = $1 AND account_id = $2 AND status_id IS NOT NULL;

-- name: ListOrphanedMediaAttachments :many
SELECT * FROM media_attachments
WHERE status_id IS NULL
  AND scheduled_status_id IS NULL
  AND created_at < @before
ORDER BY id ASC
LIMIT sqlc.arg('limit');

-- name: DeleteMediaAttachmentByID :exec
DELETE FROM media_attachments WHERE id = $1;
