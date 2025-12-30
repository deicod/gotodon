-- Metrics: dimensions
-- name: AdminMetricsLanguages :many
SELECT locale, count(*) AS value
FROM users
WHERE current_sign_in_at BETWEEN @start_at AND @end_at
  AND locale IS NOT NULL
GROUP BY locale
ORDER BY count(*) DESC
LIMIT sqlc.arg('limit');

-- name: AdminMetricsSources :many
SELECT oauth_applications.name, count(*) AS value
FROM users
LEFT JOIN oauth_applications ON oauth_applications.id = users.created_by_application_id
WHERE users.created_at BETWEEN @start_at AND @end_at
GROUP BY oauth_applications.name
ORDER BY count(*) DESC
LIMIT sqlc.arg('limit');

-- name: AdminMetricsServers :many
SELECT accounts.domain, count(*) AS value
FROM statuses
INNER JOIN accounts ON accounts.id = statuses.account_id
WHERE statuses.id BETWEEN @earliest_status_id AND @latest_status_id
GROUP BY accounts.domain
ORDER BY count(*) DESC
LIMIT sqlc.arg('limit');

-- name: AdminMetricsTagServers :many
SELECT accounts.domain, count(*) AS value
FROM statuses
INNER JOIN accounts ON accounts.id = statuses.account_id
INNER JOIN statuses_tags ON statuses_tags.status_id = statuses.id
WHERE statuses_tags.tag_id = @tag_id
  AND statuses.id BETWEEN @earliest_status_id AND @latest_status_id
GROUP BY accounts.domain
ORDER BY count(*) DESC
LIMIT sqlc.arg('limit');

-- name: AdminMetricsTagLanguages :many
SELECT COALESCE(statuses.language, 'und') AS language, count(*) AS value
FROM statuses
INNER JOIN statuses_tags ON statuses_tags.status_id = statuses.id
WHERE statuses_tags.tag_id = @tag_id
  AND statuses.id BETWEEN @earliest_status_id AND @latest_status_id
GROUP BY COALESCE(statuses.language, 'und')
ORDER BY count(*) DESC
LIMIT sqlc.arg('limit');

-- name: AdminMetricsInstanceAccountsDimension :many
SELECT accounts.username, count(follows.*) AS value
FROM accounts
LEFT JOIN follows ON follows.target_account_id = accounts.id
WHERE accounts.domain = @domain
GROUP BY accounts.id
ORDER BY value DESC
LIMIT sqlc.arg('limit');

-- name: AdminMetricsInstanceLanguages :many
SELECT COALESCE(statuses.language, 'und') AS language, count(*) AS value
FROM statuses
INNER JOIN accounts ON accounts.id = statuses.account_id
WHERE accounts.domain = @domain
  AND statuses.id BETWEEN @earliest_status_id AND @latest_status_id
  AND statuses.reblog_of_id IS NULL
GROUP BY COALESCE(statuses.language, 'und')
ORDER BY count(*) DESC
LIMIT sqlc.arg('limit');

-- name: AdminMetricsPostgresSize :one
SELECT pg_database_size(current_database()) AS size;

-- name: AdminMetricsMediaSizeComponents :one
SELECT
  COALESCE((SELECT SUM(COALESCE(file_file_size, 0) + COALESCE(thumbnail_file_size, 0)) FROM media_attachments), 0)::bigint AS media_attachments_size,
  COALESCE((SELECT SUM(image_file_size) FROM custom_emojis), 0)::bigint AS custom_emojis_size,
  COALESCE((SELECT SUM(image_file_size) FROM preview_cards), 0)::bigint AS preview_cards_size,
  COALESCE((SELECT SUM(COALESCE(avatar_file_size, 0) + COALESCE(header_file_size, 0)) FROM accounts), 0)::bigint AS account_media_size,
  COALESCE((SELECT SUM(dump_file_size) FROM backups), 0)::bigint AS backups_size,
  COALESCE((SELECT SUM(file_file_size) FROM site_uploads), 0)::bigint AS site_uploads_size;

-- name: AdminMetricsPostgresVersion :one
SELECT version() AS version;

-- Metrics: measures
-- name: AdminMetricsActiveUsersTotal :one
SELECT COUNT(DISTINCT user_id) AS value
FROM login_activities
WHERE success = true
  AND created_at BETWEEN @start_at AND @end_at;

-- name: AdminMetricsActiveUsersByDay :many
SELECT date_trunc('day', created_at)::date AS period,
       COUNT(DISTINCT user_id) AS value
FROM login_activities
WHERE success = true
  AND created_at BETWEEN @start_at AND @end_at
GROUP BY period
ORDER BY period;

-- name: AdminMetricsNewUsersTotal :one
SELECT COUNT(*) AS value
FROM users
WHERE created_at BETWEEN @start_at AND @end_at;

-- name: AdminMetricsNewUsersByDay :many
SELECT date_trunc('day', created_at)::date AS period,
       COUNT(*) AS value
FROM users
WHERE created_at BETWEEN @start_at AND @end_at
GROUP BY period
ORDER BY period;

-- name: AdminMetricsOpenedReportsTotal :one
SELECT COUNT(*) AS value
FROM reports
WHERE created_at BETWEEN @start_at AND @end_at;

-- name: AdminMetricsOpenedReportsByDay :many
SELECT date_trunc('day', created_at)::date AS period,
       COUNT(*) AS value
FROM reports
WHERE created_at BETWEEN @start_at AND @end_at
GROUP BY period
ORDER BY period;

-- name: AdminMetricsResolvedReportsTotal :one
SELECT COUNT(*) AS value
FROM reports
WHERE action_taken_at IS NOT NULL
  AND action_taken_at BETWEEN @start_at AND @end_at;

-- name: AdminMetricsResolvedReportsByDay :many
SELECT date_trunc('day', action_taken_at)::date AS period,
       COUNT(*) AS value
FROM reports
WHERE action_taken_at IS NOT NULL
  AND action_taken_at BETWEEN @start_at AND @end_at
GROUP BY period
ORDER BY period;

-- name: AdminMetricsStatusesTotal :one
SELECT COUNT(*) AS value
FROM statuses
WHERE created_at BETWEEN @start_at AND @end_at;

-- name: AdminMetricsStatusesByDay :many
SELECT date_trunc('day', created_at)::date AS period,
       COUNT(*) AS value
FROM statuses
WHERE created_at BETWEEN @start_at AND @end_at
GROUP BY period
ORDER BY period;

-- name: AdminMetricsFavouritesTotal :one
SELECT COUNT(*) AS value
FROM favourites
WHERE created_at BETWEEN @start_at AND @end_at;

-- name: AdminMetricsFavouritesByDay :many
SELECT date_trunc('day', created_at)::date AS period,
       COUNT(*) AS value
FROM favourites
WHERE created_at BETWEEN @start_at AND @end_at
GROUP BY period
ORDER BY period;

-- name: AdminMetricsFollowsTotal :one
SELECT COUNT(*) AS value
FROM follows
WHERE created_at BETWEEN @start_at AND @end_at;

-- name: AdminMetricsFollowsByDay :many
SELECT date_trunc('day', created_at)::date AS period,
       COUNT(*) AS value
FROM follows
WHERE created_at BETWEEN @start_at AND @end_at
GROUP BY period
ORDER BY period;

-- name: AdminMetricsPollVotesTotal :one
SELECT COUNT(*) AS value
FROM poll_votes
WHERE created_at BETWEEN @start_at AND @end_at;

-- name: AdminMetricsPollVotesByDay :many
SELECT date_trunc('day', created_at)::date AS period,
       COUNT(*) AS value
FROM poll_votes
WHERE created_at BETWEEN @start_at AND @end_at
GROUP BY period
ORDER BY period;

-- name: AdminMetricsTagUsesTotal :one
SELECT COUNT(*) AS value
FROM statuses_tags
INNER JOIN statuses ON statuses.id = statuses_tags.status_id
WHERE statuses_tags.tag_id = @tag_id
  AND statuses.created_at BETWEEN @start_at AND @end_at;

-- name: AdminMetricsTagUsesByDay :many
SELECT date_trunc('day', statuses.created_at)::date AS period,
       COUNT(*) AS value
FROM statuses_tags
INNER JOIN statuses ON statuses.id = statuses_tags.status_id
WHERE statuses_tags.tag_id = @tag_id
  AND statuses.created_at BETWEEN @start_at AND @end_at
GROUP BY period
ORDER BY period;

-- name: AdminMetricsTagAccountsTotal :one
SELECT COUNT(DISTINCT statuses.account_id) AS value
FROM statuses_tags
INNER JOIN statuses ON statuses.id = statuses_tags.status_id
WHERE statuses_tags.tag_id = @tag_id
  AND statuses.created_at BETWEEN @start_at AND @end_at;

-- name: AdminMetricsTagAccountsByDay :many
SELECT date_trunc('day', statuses.created_at)::date AS period,
       COUNT(DISTINCT statuses.account_id) AS value
FROM statuses_tags
INNER JOIN statuses ON statuses.id = statuses_tags.status_id
WHERE statuses_tags.tag_id = @tag_id
  AND statuses.created_at BETWEEN @start_at AND @end_at
GROUP BY period
ORDER BY period;

-- name: AdminMetricsTagServersTotal :one
SELECT COUNT(DISTINCT accounts.domain) AS value
FROM statuses_tags
INNER JOIN statuses ON statuses.id = statuses_tags.status_id
INNER JOIN accounts ON accounts.id = statuses.account_id
WHERE statuses_tags.tag_id = @tag_id
  AND statuses.created_at BETWEEN @start_at AND @end_at;

-- name: AdminMetricsTagServersByDay :many
SELECT date_trunc('day', statuses.created_at)::date AS period,
       COUNT(DISTINCT accounts.domain) AS value
FROM statuses_tags
INNER JOIN statuses ON statuses.id = statuses_tags.status_id
INNER JOIN accounts ON accounts.id = statuses.account_id
WHERE statuses_tags.tag_id = @tag_id
  AND statuses.created_at BETWEEN @start_at AND @end_at
GROUP BY period
ORDER BY period;

-- name: AdminMetricsInstanceAccountsTotal :one
SELECT COUNT(*) AS value
FROM accounts
WHERE (
  CASE WHEN @include_subdomains::boolean
    THEN (accounts.domain = @domain OR accounts.domain LIKE '%.' || @domain)
    ELSE accounts.domain = @domain
  END
);

-- name: AdminMetricsInstanceAccountsByDay :many
SELECT date_trunc('day', accounts.created_at)::date AS period,
       COUNT(*) AS value
FROM accounts
WHERE (
  CASE WHEN @include_subdomains::boolean
    THEN (accounts.domain = @domain OR accounts.domain LIKE '%.' || @domain)
    ELSE accounts.domain = @domain
  END
)
  AND accounts.created_at BETWEEN @start_at AND @end_at
GROUP BY period
ORDER BY period;

-- name: AdminMetricsInstanceFollowsTotal :one
SELECT COUNT(*) AS value
FROM follows
INNER JOIN accounts ON accounts.id = follows.target_account_id
WHERE (
  CASE WHEN @include_subdomains::boolean
    THEN (accounts.domain = @domain OR accounts.domain LIKE '%.' || @domain)
    ELSE accounts.domain = @domain
  END
);

-- name: AdminMetricsInstanceFollowsByDay :many
SELECT date_trunc('day', follows.created_at)::date AS period,
       COUNT(*) AS value
FROM follows
INNER JOIN accounts ON accounts.id = follows.target_account_id
WHERE (
  CASE WHEN @include_subdomains::boolean
    THEN (accounts.domain = @domain OR accounts.domain LIKE '%.' || @domain)
    ELSE accounts.domain = @domain
  END
)
  AND follows.created_at BETWEEN @start_at AND @end_at
GROUP BY period
ORDER BY period;

-- name: AdminMetricsInstanceFollowersTotal :one
SELECT COUNT(*) AS value
FROM follows
INNER JOIN accounts ON accounts.id = follows.account_id
WHERE (
  CASE WHEN @include_subdomains::boolean
    THEN (accounts.domain = @domain OR accounts.domain LIKE '%.' || @domain)
    ELSE accounts.domain = @domain
  END
);

-- name: AdminMetricsInstanceFollowersByDay :many
SELECT date_trunc('day', follows.created_at)::date AS period,
       COUNT(*) AS value
FROM follows
INNER JOIN accounts ON accounts.id = follows.account_id
WHERE (
  CASE WHEN @include_subdomains::boolean
    THEN (accounts.domain = @domain OR accounts.domain LIKE '%.' || @domain)
    ELSE accounts.domain = @domain
  END
)
  AND follows.created_at BETWEEN @start_at AND @end_at
GROUP BY period
ORDER BY period;

-- name: AdminMetricsInstanceStatusesTotal :one
SELECT COUNT(*) AS value
FROM statuses
INNER JOIN accounts ON accounts.id = statuses.account_id
WHERE (
  CASE WHEN @include_subdomains::boolean
    THEN (accounts.domain = @domain OR accounts.domain LIKE '%.' || @domain)
    ELSE accounts.domain = @domain
  END
);

-- name: AdminMetricsInstanceStatusesByDay :many
SELECT date_trunc('day', statuses.created_at)::date AS period,
       COUNT(*) AS value
FROM statuses
INNER JOIN accounts ON accounts.id = statuses.account_id
WHERE (
  CASE WHEN @include_subdomains::boolean
    THEN (accounts.domain = @domain OR accounts.domain LIKE '%.' || @domain)
    ELSE accounts.domain = @domain
  END
)
  AND statuses.created_at BETWEEN @start_at AND @end_at
GROUP BY period
ORDER BY period;

-- name: AdminMetricsInstanceReportsTotal :one
SELECT COUNT(*) AS value
FROM reports
INNER JOIN accounts ON accounts.id = reports.target_account_id
WHERE (
  CASE WHEN @include_subdomains::boolean
    THEN (accounts.domain = @domain OR accounts.domain LIKE '%.' || @domain)
    ELSE accounts.domain = @domain
  END
);

-- name: AdminMetricsInstanceReportsByDay :many
SELECT date_trunc('day', reports.created_at)::date AS period,
       COUNT(*) AS value
FROM reports
INNER JOIN accounts ON accounts.id = reports.target_account_id
WHERE (
  CASE WHEN @include_subdomains::boolean
    THEN (accounts.domain = @domain OR accounts.domain LIKE '%.' || @domain)
    ELSE accounts.domain = @domain
  END
)
  AND reports.created_at BETWEEN @start_at AND @end_at
GROUP BY period
ORDER BY period;

-- name: AdminMetricsInstanceMediaTotal :one
SELECT COALESCE(SUM(COALESCE(media_attachments.file_file_size, 0) + COALESCE(media_attachments.thumbnail_file_size, 0)), 0)::bigint AS value
FROM media_attachments
INNER JOIN accounts ON accounts.id = media_attachments.account_id
WHERE (
  CASE WHEN @include_subdomains::boolean
    THEN (accounts.domain = @domain OR accounts.domain LIKE '%.' || @domain)
    ELSE accounts.domain = @domain
  END
);

-- name: AdminMetricsInstanceMediaByDay :many
SELECT date_trunc('day', media_attachments.created_at)::date AS period,
       COALESCE(SUM(COALESCE(media_attachments.file_file_size, 0) + COALESCE(media_attachments.thumbnail_file_size, 0)), 0)::bigint AS value
FROM media_attachments
INNER JOIN accounts ON accounts.id = media_attachments.account_id
WHERE (
  CASE WHEN @include_subdomains::boolean
    THEN (accounts.domain = @domain OR accounts.domain LIKE '%.' || @domain)
    ELSE accounts.domain = @domain
  END
)
  AND media_attachments.created_at BETWEEN @start_at AND @end_at
GROUP BY period
ORDER BY period;

-- Metrics: retention
-- name: AdminMetricsRetention :many
WITH cohort_periods AS (
  SELECT generate_series(
    date_trunc(@frequency, @start_at::timestamp)::date,
    date_trunc(@frequency, @end_at::timestamp)::date,
    ('1 ' || @frequency)::interval
  )::date AS cohort_period
),
retention_periods AS (
  SELECT cohort_period AS retention_period FROM cohort_periods
),
axis AS (
  SELECT * FROM cohort_periods, retention_periods
  WHERE retention_period >= cohort_period
)
SELECT axis.cohort_period,
       axis.retention_period,
       (
         SELECT COUNT(*)
         FROM users
         WHERE date_trunc(@frequency, users.created_at)::date = axis.cohort_period
           AND date_trunc(@frequency, users.current_sign_in_at) >= axis.retention_period
       ) AS value,
       (
         SELECT CASE
           WHEN COUNT(*) = 0 THEN 0::double precision
           ELSE (
             SELECT COUNT(*)
             FROM users
             WHERE date_trunc(@frequency, users.created_at)::date = axis.cohort_period
               AND date_trunc(@frequency, users.current_sign_in_at) >= axis.retention_period
           )::double precision / COUNT(*)
         END
         FROM users
         WHERE date_trunc(@frequency, users.created_at)::date = axis.cohort_period
       )::double precision AS rate
FROM axis
ORDER BY axis.cohort_period, axis.retention_period;
