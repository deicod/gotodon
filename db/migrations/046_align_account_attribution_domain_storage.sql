-- Migration 046: Align Account Attribution Domain Storage
-- Expands accounts.attribution_domains to unconstrained text[] to match Mastodon's string array behavior.

-- +goose Up

ALTER TABLE accounts
    ALTER COLUMN attribution_domains TYPE text[]
    USING attribution_domains::text[];

-- +goose Down

ALTER TABLE accounts
    ALTER COLUMN attribution_domains TYPE varchar(255)[]
    USING attribution_domains::varchar(255)[];
