-- Migration 025: Web Settings and Webhooks
-- Adds web_settings and webhooks tables.

-- +goose Up

CREATE TABLE web_settings (
    id bigserial PRIMARY KEY,
    data json,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    user_id bigint NOT NULL,

    CONSTRAINT fk_web_settings_user FOREIGN KEY (user_id)
        REFERENCES users(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX index_web_settings_on_user_id
    ON web_settings (user_id);

CREATE TABLE webhooks (
    id bigserial PRIMARY KEY,
    url varchar(255) NOT NULL,
    events varchar(255)[] NOT NULL DEFAULT '{}',
    secret varchar(255) NOT NULL DEFAULT '',
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    template text
);

CREATE UNIQUE INDEX index_webhooks_on_url
    ON webhooks (url);

-- +goose Down

DROP TABLE IF EXISTS webhooks CASCADE;
DROP TABLE IF EXISTS web_settings CASCADE;
