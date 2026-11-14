-- name: AdminGetAccountWarningPreset :one
-- The warning preset that Admin::AccountAction#warning_preset finds.
SELECT * FROM account_warning_presets WHERE id = $1;

-- name: AdminListUnresolvedReportsByTarget :many
-- The reports that Admin::AccountAction#reports resolves for every type but
-- none: Report.where(target_account:).unresolved. FOR UPDATE keeps a
-- concurrent resolution or reopening of one of them from interleaving with
-- the action.
SELECT * FROM reports
WHERE target_account_id = $1 AND action_taken_at IS NULL
ORDER BY id
FOR UPDATE;

-- name: CreateAccountWarning :one
-- A strike, as Admin::BaseAction#process_strike! creates it. account_id and
-- report_id are NULL without an acting account or report, and status_ids is
-- NULL without a report.
INSERT INTO account_warnings (
    account_id,
    target_account_id,
    report_id,
    action,
    text,
    status_ids,
    created_at,
    updated_at
) VALUES (
    sqlc.narg('account_id'),
    @target_account_id,
    sqlc.narg('report_id'),
    @action,
    @text,
    sqlc.narg('status_ids')::varchar[],
    now(),
    now()
)
RETURNING *;

-- name: GetAccountWarningsByIDs :many
-- The strikes that moderation_warning notifications render
-- (REST::AccountWarningSerializer), with their appeal, if any: the appeal
-- columns are NULL for a strike without one.
SELECT
    w.id,
    w.target_account_id,
    w.action,
    w.text,
    w.status_ids,
    w.created_at,
    ap.id AS appeal_id,
    ap.text AS appeal_text,
    ap.approved_at AS appeal_approved_at,
    ap.rejected_at AS appeal_rejected_at
FROM account_warnings w
LEFT JOIN appeals ap ON ap.account_warning_id = w.id
WHERE w.id = ANY(@ids::bigint[])
ORDER BY w.id;

-- name: AdminListCitedStatuses :many
-- The statuses a strike cites, deleted ones included as in
-- AccountWarning#statuses (Status.with_discarded), with their authors'
-- usernames, for the warning e-mail's links.
SELECT
    s.id,
    s.uri,
    s.url,
    a.username,
    a.domain
FROM statuses s
JOIN accounts a ON a.id = s.account_id
WHERE s.id = ANY(@ids::bigint[])
ORDER BY s.id;
