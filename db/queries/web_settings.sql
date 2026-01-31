-- name: GetWebSettingByUserID :one
SELECT * FROM web_settings WHERE user_id = $1;

-- name: UpsertWebSetting :one
INSERT INTO web_settings (data, user_id, created_at, updated_at)
VALUES ($1, $2, now(), now())
ON CONFLICT (user_id) DO UPDATE
SET data = EXCLUDED.data, updated_at = now()
RETURNING *;
