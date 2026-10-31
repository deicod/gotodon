-- Migration 038: Align Column Types
-- Aligns polls.cached_tallies to Mastodon bigint[] array.
--
-- PostgreSQL rejects subqueries in ALTER COLUMN ... USING, so the jsonb to
-- bigint[] conversion goes through a helper function that the migration drops
-- again. The column default is dropped before each type change because a jsonb
-- default cannot be cast to bigint[] and back.
--
-- Arrays keep their order, null elements become 0 and any other jsonb value
-- (such as the original '{}' default) becomes an empty array.

-- +goose Up

ALTER TABLE polls ALTER COLUMN cached_tallies DROP DEFAULT;

-- +goose StatementBegin
CREATE FUNCTION gotodon_jsonb_to_bigint_array(value jsonb) RETURNS bigint[]
    LANGUAGE sql IMMUTABLE
AS $$
    SELECT CASE
        WHEN jsonb_typeof(value) = 'array' THEN COALESCE(
            (SELECT array_agg(COALESCE(element::bigint, 0) ORDER BY ord)
             FROM jsonb_array_elements_text(value) WITH ORDINALITY AS tallies(element, ord)),
            '{}'::bigint[])
        ELSE '{}'::bigint[]
    END
$$;
-- +goose StatementEnd

ALTER TABLE polls
    ALTER COLUMN cached_tallies TYPE bigint[]
    USING gotodon_jsonb_to_bigint_array(cached_tallies);

DROP FUNCTION gotodon_jsonb_to_bigint_array(jsonb);

ALTER TABLE polls
    ALTER COLUMN cached_tallies SET DEFAULT '{}'::bigint[],
    ALTER COLUMN cached_tallies SET NOT NULL;

-- +goose Down

ALTER TABLE polls ALTER COLUMN cached_tallies DROP DEFAULT;

ALTER TABLE polls
    ALTER COLUMN cached_tallies TYPE jsonb
    USING to_jsonb(cached_tallies);

ALTER TABLE polls
    ALTER COLUMN cached_tallies SET DEFAULT '{}'::jsonb,
    ALTER COLUMN cached_tallies SET NOT NULL;
