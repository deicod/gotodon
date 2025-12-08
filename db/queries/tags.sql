-- name: GetTagByName :one
SELECT * FROM tags WHERE lower(name) = lower($1);

-- name: CreateTag :one
INSERT INTO tags (name, created_at, updated_at)
VALUES ($1, now(), now())
RETURNING *;
