-- Migration 042: Align OAuth Application Website Length
-- Expands oauth_applications.website to match Mastodon's 2000-character validation limit.

-- +goose Up

ALTER TABLE oauth_applications
    ALTER COLUMN website TYPE varchar(2000);

-- +goose Down

ALTER TABLE oauth_applications
    ALTER COLUMN website TYPE varchar(255);
