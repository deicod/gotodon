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

-- name: ListTagHistories :many
WITH days AS (
    SELECT generate_series(
        date_trunc('day', now()) - interval '6 days',
        date_trunc('day', now()),
        interval '1 day'
    )::date AS day
),
tag_days AS (
    SELECT t.tag_id::bigint AS tag_id, d.day
    FROM unnest(@tag_ids::bigint[]) AS t(tag_id)
    CROSS JOIN days d
)
SELECT
    td.tag_id,
    td.day,
    COALESCE(COUNT(st.status_id), 0)::bigint AS uses,
    COALESCE(COUNT(DISTINCT s.account_id), 0)::bigint AS accounts
FROM tag_days td
LEFT JOIN statuses_tags st ON st.tag_id = td.tag_id
LEFT JOIN statuses s ON s.id = st.status_id
    AND s.created_at >= td.day
    AND s.created_at < td.day + interval '1 day'
    AND s.deleted_at IS NULL
    AND s.visibility IN (0, 1)
GROUP BY td.tag_id, td.day
ORDER BY td.tag_id, td.day;
