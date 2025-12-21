-- Migration 009: Fix account_migrations.account_id to be NOT NULL
-- This fixes a schema issue where account_migrations.account_id was nullable,
-- which breaks referential integrity since every migration must belong to an account.

-- +goose Up

-- First, delete any orphaned rows with null account_id
DELETE FROM account_migrations WHERE account_id IS NULL;

-- Then add the NOT NULL constraint
ALTER TABLE account_migrations ALTER COLUMN account_id SET NOT NULL;

-- +goose Down

-- Revert to nullable (not recommended in production)
ALTER TABLE account_migrations ALTER COLUMN account_id DROP NOT NULL;
