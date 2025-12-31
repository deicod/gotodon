-- name: GetSettingsByVars :many
SELECT var, value, updated_at
FROM settings
WHERE var = ANY(@vars::text[]);

-- name: UpsertSetting :exec
INSERT INTO settings (var, value, created_at, updated_at)
VALUES ($1, $2, now(), now())
ON CONFLICT (var) DO UPDATE
SET value = EXCLUDED.value, updated_at = now();
