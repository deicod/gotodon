-- Status cleanup queries
-- name: GetStatusCleanupCutoffID :one
SELECT id FROM (
    SELECT id FROM statuses
    WHERE account_id = @account_id
      AND deleted_at IS NULL
      AND id >= @min_id
      AND id <= @max_id
    ORDER BY id ASC
    LIMIT sqlc.arg('limit')
) t
ORDER BY id DESC
LIMIT 1;

-- name: ListStatusesForCleanup :many
SELECT s.* FROM statuses s
LEFT JOIN status_stats ss ON ss.status_id = s.id
WHERE s.account_id = @account_id
  AND s.deleted_at IS NULL
  AND s.id <= @max_id
  AND (@min_id::bigint = 0 OR s.id >= @min_id)
  AND (@keep_direct::boolean = false OR s.visibility != 3)
  AND (@keep_pinned::boolean = false OR NOT EXISTS (
    SELECT 1 FROM status_pins sp
    WHERE sp.account_id = s.account_id AND sp.status_id = s.id
  ))
  AND (@keep_polls::boolean = false OR s.poll_id IS NULL)
  AND (@keep_media::boolean = false OR NOT EXISTS (
    SELECT 1 FROM media_attachments ma WHERE ma.status_id = s.id
  ))
  AND (@keep_self_fav::boolean = false OR NOT EXISTS (
    SELECT 1 FROM favourites f WHERE f.account_id = s.account_id AND f.status_id = s.id
  ))
  AND (@keep_self_bookmark::boolean = false OR NOT EXISTS (
    SELECT 1 FROM bookmarks b WHERE b.account_id = s.account_id AND b.status_id = s.id
  ))
  AND (sqlc.narg('min_favs')::int4 IS NULL OR COALESCE(ss.favourites_count, 0) < sqlc.narg('min_favs')::int4)
  AND (sqlc.narg('min_reblogs')::int4 IS NULL OR COALESCE(ss.reblogs_count, 0) < sqlc.narg('min_reblogs')::int4)
ORDER BY s.id ASC
LIMIT sqlc.arg('limit');
