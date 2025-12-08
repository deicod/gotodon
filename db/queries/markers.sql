-- name: GetMarker :one
SELECT * FROM markers WHERE user_id = $1 AND timeline = $2;

-- name: UpsertMarker :one
INSERT INTO markers (user_id, timeline, last_read_id, lock_version, updated_at)
VALUES ($1, $2, $3, $4, now())
ON CONFLICT (user_id, timeline) DO UPDATE
SET last_read_id = EXCLUDED.last_read_id,
    lock_version = EXCLUDED.lock_version,
    updated_at = now()
RETURNING *;
