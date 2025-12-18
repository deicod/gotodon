-- name: CreateMention :one
INSERT INTO mentions (
    status_id, account_id, created_at, updated_at, silent
) VALUES (
    $1, $2, NOW(), NOW(), $3
) RETURNING *;

-- name: GetMentionsByStatusID :many
SELECT * FROM mentions WHERE status_id = $1;

-- name: GetMentionsByAccountID :many
SELECT * FROM mentions WHERE account_id = $1 ORDER BY id DESC LIMIT $2 OFFSET $3;

-- name: CreateMentions :exec
INSERT INTO mentions (status_id, account_id, created_at, updated_at, silent)
SELECT $1, unnest($2::bigint[]), now(), now(), unnest($3::boolean[])
ON CONFLICT DO NOTHING;

-- name: RemoveMentions :exec
DELETE FROM mentions WHERE status_id = $1 AND account_id = ANY($2::bigint[]);
