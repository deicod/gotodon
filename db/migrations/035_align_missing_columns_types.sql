-- Migration 035: Align Missing Columns and Types
-- Adds missing columns and reconciles type differences.

-- +goose Up

ALTER TABLE statuses
    ADD COLUMN quote_approval_policy integer NOT NULL DEFAULT 0;

ALTER TABLE reports
    ADD COLUMN application_id bigint;

ALTER TABLE reports
    ALTER COLUMN rule_ids TYPE bigint[] USING rule_ids::bigint[],
    ALTER COLUMN rule_ids DROP NOT NULL,
    ALTER COLUMN rule_ids DROP DEFAULT;

ALTER TABLE reports
    ADD CONSTRAINT fk_reports_application FOREIGN KEY (application_id)
        REFERENCES oauth_applications(id) ON DELETE SET NULL;

ALTER TABLE users
    ADD COLUMN age_verified_at timestamptz,
    ADD COLUMN require_tos_interstitial boolean NOT NULL DEFAULT false;

ALTER TABLE web_push_subscriptions
    ADD COLUMN standard boolean NOT NULL DEFAULT false,
    ALTER COLUMN data TYPE json USING data::json;

ALTER TABLE media_attachments
    ALTER COLUMN file_meta TYPE json USING file_meta::json;

-- +goose Down

ALTER TABLE media_attachments
    ALTER COLUMN file_meta TYPE jsonb USING file_meta::jsonb;

ALTER TABLE web_push_subscriptions
    ALTER COLUMN data TYPE jsonb USING data::jsonb,
    DROP COLUMN IF EXISTS standard;

ALTER TABLE users
    DROP COLUMN IF EXISTS require_tos_interstitial,
    DROP COLUMN IF EXISTS age_verified_at;

ALTER TABLE reports
    DROP CONSTRAINT IF EXISTS fk_reports_application;

ALTER TABLE reports
    ALTER COLUMN rule_ids TYPE integer[] USING rule_ids::integer[],
    ALTER COLUMN rule_ids SET NOT NULL,
    ALTER COLUMN rule_ids SET DEFAULT '{}';

ALTER TABLE reports
    DROP COLUMN IF EXISTS application_id;

ALTER TABLE statuses
    DROP COLUMN IF EXISTS quote_approval_policy;
