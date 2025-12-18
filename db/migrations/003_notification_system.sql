-- Migration 003: Notification System Tables
-- Adds notification_policies, notification_requests, and notification_permissions
-- Based on Mastodon v4.5.3 schema

-- +goose Up

-- ============================================================================
-- Notification Permissions (allow list for notifications from specific accounts)
-- ============================================================================

CREATE TABLE notification_permissions (
    id bigserial PRIMARY KEY,
    account_id bigint NOT NULL,
    from_account_id bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    
    CONSTRAINT fk_notification_permissions_account FOREIGN KEY (account_id) 
        REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT fk_notification_permissions_from_account FOREIGN KEY (from_account_id) 
        REFERENCES accounts(id) ON DELETE CASCADE
);

CREATE INDEX index_notification_permissions_on_account_id 
    ON notification_permissions (account_id);
CREATE INDEX index_notification_permissions_on_from_account_id 
    ON notification_permissions (from_account_id);
CREATE UNIQUE INDEX index_notification_permissions_unique 
    ON notification_permissions (account_id, from_account_id);

-- ============================================================================
-- Notification Policies (per-account notification filtering rules)
-- ============================================================================

-- Policy values:
-- 0 = accept (show notifications)
-- 1 = filter (send to notification requests)
-- 2 = drop (silently ignore)

CREATE TABLE notification_policies (
    id bigserial PRIMARY KEY,
    account_id bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    for_not_following integer NOT NULL DEFAULT 0,
    for_not_followers integer NOT NULL DEFAULT 0,
    for_new_accounts integer NOT NULL DEFAULT 0,
    for_private_mentions integer NOT NULL DEFAULT 1,
    for_limited_accounts integer NOT NULL DEFAULT 1,
    
    CONSTRAINT fk_notification_policies_account FOREIGN KEY (account_id) 
        REFERENCES accounts(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX index_notification_policies_on_account_id 
    ON notification_policies (account_id);

-- ============================================================================
-- Notification Requests (pending notification permission requests)
-- ============================================================================

CREATE SEQUENCE notification_requests_id_seq;
CREATE TABLE notification_requests (
    id bigint PRIMARY KEY DEFAULT timestamp_id('notification_requests'),
    account_id bigint NOT NULL,
    from_account_id bigint NOT NULL,
    last_status_id bigint,
    notifications_count bigint NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    
    CONSTRAINT fk_notification_requests_account FOREIGN KEY (account_id) 
        REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT fk_notification_requests_from_account FOREIGN KEY (from_account_id) 
        REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT fk_notification_requests_last_status FOREIGN KEY (last_status_id) 
        REFERENCES statuses(id) ON DELETE SET NULL
);

CREATE UNIQUE INDEX index_notification_requests_on_account_id_and_from_account_id 
    ON notification_requests (account_id, from_account_id);
CREATE INDEX index_notification_requests_on_from_account_id 
    ON notification_requests (from_account_id);
CREATE INDEX index_notification_requests_on_last_status_id 
    ON notification_requests (last_status_id);

-- ============================================================================
-- Update notifications table with missing columns
-- ============================================================================

-- Add group_key column if not exists (for grouped notifications in v2 API)
ALTER TABLE notifications ADD COLUMN IF NOT EXISTS group_key varchar(255);

-- Add index for grouped notifications
CREATE INDEX IF NOT EXISTS index_notifications_on_account_id_and_group_key 
    ON notifications (account_id, group_key) WHERE group_key IS NOT NULL;

-- +goose Down

DROP INDEX IF EXISTS index_notifications_on_account_id_and_group_key;
ALTER TABLE notifications DROP COLUMN IF EXISTS group_key;

DROP TABLE IF EXISTS notification_requests CASCADE;
DROP SEQUENCE IF EXISTS notification_requests_id_seq;
DROP TABLE IF EXISTS notification_policies CASCADE;
DROP TABLE IF EXISTS notification_permissions CASCADE;
