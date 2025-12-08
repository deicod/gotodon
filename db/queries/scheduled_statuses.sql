-- name: CreateScheduledStatus :one
INSERT INTO scheduled_statuses (
    account_id, scheduled_at, params
) VALUES (
    $1, $2, $3
) RETURNING *;

-- name: GetScheduledStatus :one
SELECT * FROM scheduled_statuses WHERE id = $1 AND account_id = $2;

-- name: ListScheduledStatuses :many
SELECT * FROM scheduled_statuses 
WHERE account_id = $1 
ORDER BY scheduled_at ASC
LIMIT $2 OFFSET $3;

-- name: UpdateScheduledStatus :one
UPDATE scheduled_statuses SET
    scheduled_at = COALESCE(sqlc.narg('scheduled_at'), scheduled_at),
    updated_at = now()
WHERE id = $1 AND account_id = $2
RETURNING *;

-- name: DeleteScheduledStatus :exec
DELETE FROM scheduled_statuses WHERE id = $1 AND account_id = $2;

-- name: ListDueScheduledStatuses :many
SELECT * FROM scheduled_statuses 
WHERE scheduled_at <= now() 
ORDER BY scheduled_at ASC;
