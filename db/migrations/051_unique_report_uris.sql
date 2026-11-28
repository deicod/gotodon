-- Migration 051: Unique Report URIs
-- A report that an inbound Flag activity files keeps the activity's id in
-- reports.uri. A Flag names one or more target accounts, and Mastodon's
-- ActivityPub::Activity::Flag files one report per target under the same id,
-- so the reporter, the target and the id together name one report. This
-- partial index makes the database refuse a second copy of it, so a Flag
-- delivered twice, or twice at once, files its reports once. Reports without
-- a uri are not affected.
--
-- No report has carried a uri before this migration, so it cannot fail on
-- existing rows.

-- +goose Up
CREATE UNIQUE INDEX index_reports_on_account_id_and_target_account_id_and_uri
    ON reports (account_id, target_account_id, uri)
    WHERE uri IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS index_reports_on_account_id_and_target_account_id_and_uri;
