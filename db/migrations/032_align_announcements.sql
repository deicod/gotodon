-- Migration 032: Align Announcements
-- Adds notification_sent_at to announcements.

-- +goose Up

ALTER TABLE announcements
    ADD COLUMN notification_sent_at timestamptz;

-- +goose Down

ALTER TABLE announcements
    DROP COLUMN IF EXISTS notification_sent_at;
