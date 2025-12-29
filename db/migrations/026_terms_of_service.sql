-- Migration 026: Terms of Service
-- Adds terms_of_services table.

-- +goose Up

CREATE TABLE terms_of_services (
    id bigserial PRIMARY KEY,
    text text NOT NULL DEFAULT '',
    changelog text NOT NULL DEFAULT '',
    published_at timestamptz,
    notification_sent_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    effective_date date
);

CREATE UNIQUE INDEX index_terms_of_services_on_effective_date
    ON terms_of_services (effective_date)
    WHERE effective_date IS NOT NULL;

-- +goose Down

DROP TABLE IF EXISTS terms_of_services CASCADE;
