-- Migration 019: Custom Emoji Categories
-- Adds custom_emoji_categories table.

-- +goose Up

CREATE TABLE custom_emoji_categories (
    id bigserial PRIMARY KEY,
    name varchar(255),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX index_custom_emoji_categories_on_name
    ON custom_emoji_categories (name);

-- +goose Down

DROP TABLE IF EXISTS custom_emoji_categories CASCADE;
