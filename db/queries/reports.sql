-- name: CreateReport :one
INSERT INTO reports (
    account_id, target_account_id, status_ids, comment, category, forwarded, created_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6, now(), now()
)
RETURNING *;

-- name: AdminListReports :many
SELECT 
    r.*,
    a.username as account_username, a.domain as account_domain,
    ta.username as target_account_username, ta.domain as target_account_domain
FROM reports r
JOIN accounts a ON r.account_id = a.id
JOIN accounts ta ON r.target_account_id = ta.id
WHERE 
    (CASE WHEN @resolved::boolean THEN r.action_taken_at IS NOT NULL ELSE r.action_taken_at IS NULL END)
    AND (CASE WHEN @account_id::bigint > 0 THEN r.account_id = @account_id ELSE TRUE END)
    AND (CASE WHEN @target_account_id::bigint > 0 THEN r.target_account_id = @target_account_id ELSE TRUE END)
ORDER BY r.created_at DESC
LIMIT $1 OFFSET $2;

-- name: AdminGetReport :one
SELECT * FROM reports WHERE id = $1;

-- name: AdminResolveReport :exec
UPDATE reports 
SET action_taken_by_account_id = $2, action_taken_at = now(), updated_at = now()
WHERE id = $1;

-- name: AdminAssignReport :exec
UPDATE reports 
SET assigned_account_id = $2, updated_at = now()
WHERE id = $1;

-- name: AdminUnresolveReport :exec
UPDATE reports
SET action_taken_by_account_id = NULL, action_taken_at = NULL, updated_at = now()
WHERE id = $1;

-- name: AdminUpdateReport :one
UPDATE reports
SET category = COALESCE(sqlc.narg('category'), category),
    rule_ids = COALESCE(sqlc.narg('rule_ids'), rule_ids),
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: CreateRemoteReport :one
-- CreateRemoteReport files the report of a remote account that an inbound
-- Flag activity, whose id is uri, makes, as Mastodon's ReportService does for
-- a remote reporter: category other, not forwarded. It returns no row when
-- the account's report of the target under uri exists already, so a Flag
-- delivered twice files its report once.
INSERT INTO reports (
    account_id, target_account_id, status_ids, comment, category, forwarded, uri, created_at, updated_at
) VALUES (
    @account_id, @target_account_id, @status_ids, @comment, 0, false, @uri::varchar, now(), now()
)
ON CONFLICT (account_id, target_account_id, uri) WHERE uri IS NOT NULL DO NOTHING
RETURNING *;

-- name: ListReportableStatusIDs :many
-- ListReportableStatusIDs returns, in id order, the statuses among status_ids
-- that account_id wrote, that are not deleted, and that the server at domain
-- could have received: public and unlisted ones, private ones when an account
-- of domain follows the author, and any that mention an account of domain.
-- Mastodon's ReportService#reported_status_ids attaches only these to the
-- report of a remote reporter, whose server cannot know of others.
SELECT s.id FROM statuses s
WHERE s.account_id = @account_id
  AND s.id = ANY(@status_ids::bigint[])
  AND s.deleted_at IS NULL
  AND (
    s.visibility IN (0, 1)
    OR (s.visibility = 2 AND EXISTS (
        SELECT 1 FROM follows f
        JOIN accounts fa ON fa.id = f.account_id
        WHERE f.target_account_id = s.account_id
          AND lower(fa.domain) = lower(@domain::text)
    ))
    OR EXISTS (
        SELECT 1 FROM mentions m
        JOIN accounts ma ON ma.id = m.account_id
        WHERE m.status_id = s.id
          AND lower(ma.domain) = lower(@domain::text)
    )
  )
ORDER BY s.id;
