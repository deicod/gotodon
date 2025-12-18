-- name: GetTagByName :one
SELECT * FROM tags WHERE lower(name) = lower($1);

-- name: CreateTag :one
INSERT INTO tags (name, created_at, updated_at)
VALUES ($1, now(), now())
RETURNING *;

-- name: AddStatusTags :exec
INSERT INTO statuses_tags (status_id, tag_id)
SELECT $1, unnest($2::bigint[])
ON CONFLICT DO NOTHING;

-- name: GetTagsByNames :many
SELECT * FROM tags WHERE name = ANY($1::text[]);

-- name: UpsertTags :exec
INSERT INTO tags (name, created_at, updated_at)
SELECT unnest($1::text[]), now(), now()
ON CONFLICT (name) DO UPDATE SET updated_at = now();

-- name: RemoveStatusTags :exec
DELETE FROM statuses_tags WHERE status_id = $1 AND tag_id = ANY($2::bigint[]);

-- name: ClearStatusTags :exec
DELETE FROM statuses_tags WHERE status_id = $1;
