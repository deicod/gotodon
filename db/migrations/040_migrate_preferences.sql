-- Migration 040: Migrate Preferences to User Settings
-- Moves data from preferences into users.settings and drops the preferences table.

-- +goose Up

CREATE OR REPLACE FUNCTION safe_jsonb(value text) RETURNS jsonb AS $$
BEGIN
    IF value IS NULL OR btrim(value) = '' THEN
        RETURN '{}'::jsonb;
    END IF;
    RETURN value::jsonb;
EXCEPTION WHEN others THEN
    RETURN '{}'::jsonb;
END;
$$ LANGUAGE plpgsql IMMUTABLE;

WITH prefs AS (
    SELECT account_id,
           posting_default_visibility,
           posting_default_sensitive,
           posting_default_language,
           reading_expand_media,
           reading_expand_spoilers
    FROM preferences
)
UPDATE users u
SET settings = (
    SELECT (
        safe_jsonb(u.settings)
        || jsonb_strip_nulls(
            jsonb_build_object(
                'default_privacy', prefs.posting_default_visibility,
                'default_sensitive', prefs.posting_default_sensitive,
                'default_language', prefs.posting_default_language,
                'web.display_media', prefs.reading_expand_media,
                'web.expand_content_warnings', prefs.reading_expand_spoilers
            )
        )
    )::text
    FROM prefs
    WHERE prefs.account_id = u.account_id
)
WHERE EXISTS (
    SELECT 1 FROM prefs WHERE prefs.account_id = u.account_id
);

DROP FUNCTION IF EXISTS safe_jsonb(text);

DROP TABLE IF EXISTS preferences CASCADE;

-- +goose Down

CREATE TABLE preferences (
    id bigserial PRIMARY KEY,
    account_id bigint NOT NULL UNIQUE,
    posting_default_visibility varchar(255) DEFAULT 'public',
    posting_default_sensitive boolean DEFAULT false,
    posting_default_language varchar(255),
    reading_expand_media varchar(255) DEFAULT 'default',
    reading_expand_spoilers boolean DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT fk_preferences_account FOREIGN KEY (account_id)
        REFERENCES accounts(id) ON DELETE CASCADE
);

CREATE OR REPLACE FUNCTION safe_jsonb(value text) RETURNS jsonb AS $$
BEGIN
    IF value IS NULL OR btrim(value) = '' THEN
        RETURN '{}'::jsonb;
    END IF;
    RETURN value::jsonb;
EXCEPTION WHEN others THEN
    RETURN '{}'::jsonb;
END;
$$ LANGUAGE plpgsql IMMUTABLE;

INSERT INTO preferences (
    account_id,
    posting_default_visibility,
    posting_default_sensitive,
    posting_default_language,
    reading_expand_media,
    reading_expand_spoilers,
    created_at,
    updated_at
)
SELECT
    u.account_id,
    COALESCE(settings.data->>'default_privacy', 'public'),
    COALESCE((settings.data->>'default_sensitive')::boolean, false),
    settings.data->>'default_language',
    COALESCE(settings.data->>'web.display_media', 'default'),
    COALESCE((settings.data->>'web.expand_content_warnings')::boolean, false),
    now(),
    now()
FROM users u
LEFT JOIN LATERAL (
    SELECT safe_jsonb(u.settings) AS data
) settings ON true;

DROP FUNCTION IF EXISTS safe_jsonb(text);
