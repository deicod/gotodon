-- Migration 038: Align Column Types
-- Aligns polls.cached_tallies to Mastodon bigint[] array.

-- +goose Up

ALTER TABLE polls
    ALTER COLUMN cached_tallies TYPE bigint[]
    USING COALESCE(
        (SELECT array_agg(value::bigint) FROM jsonb_array_elements_text(cached_tallies) AS value),
        '{}'
    );

ALTER TABLE polls
    ALTER COLUMN cached_tallies SET DEFAULT '{}'::bigint[],
    ALTER COLUMN cached_tallies SET NOT NULL;

-- +goose Down

ALTER TABLE polls
    ALTER COLUMN cached_tallies TYPE jsonb
    USING to_jsonb(cached_tallies);

ALTER TABLE polls
    ALTER COLUMN cached_tallies SET DEFAULT '{}'::jsonb,
    ALTER COLUMN cached_tallies SET NOT NULL;
