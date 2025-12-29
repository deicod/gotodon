-- Migration 034: Align Scheduled Statuses
-- Aligns defaults, nullability, and drops timestamps.

-- +goose Up

ALTER TABLE scheduled_statuses
    ALTER COLUMN id SET DEFAULT nextval('scheduled_statuses_id_seq'),
    ALTER COLUMN scheduled_at DROP NOT NULL,
    ALTER COLUMN params DROP NOT NULL,
    ALTER COLUMN params DROP DEFAULT;

ALTER TABLE scheduled_statuses
    DROP COLUMN created_at,
    DROP COLUMN updated_at;

-- +goose Down

ALTER TABLE scheduled_statuses
    ADD COLUMN created_at timestamptz NOT NULL DEFAULT now(),
    ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();

ALTER TABLE scheduled_statuses
    ALTER COLUMN params SET DEFAULT '{}',
    ALTER COLUMN params SET NOT NULL,
    ALTER COLUMN scheduled_at SET NOT NULL,
    ALTER COLUMN id SET DEFAULT timestamp_id('scheduled_statuses');
