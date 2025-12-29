-- Migration 018: Custom Filters
-- Adds custom_filters, custom_filter_keywords, and custom_filter_statuses.

-- +goose Up

CREATE TABLE custom_filters (
    id bigserial PRIMARY KEY,
    account_id bigint NOT NULL,
    expires_at timestamptz,
    phrase text NOT NULL DEFAULT '',
    context varchar(255)[] NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    action integer NOT NULL DEFAULT 0,

    CONSTRAINT fk_custom_filters_account FOREIGN KEY (account_id)
        REFERENCES accounts(id) ON DELETE CASCADE
);

CREATE INDEX index_custom_filters_on_account_id
    ON custom_filters (account_id);

CREATE TABLE custom_filter_keywords (
    id bigserial PRIMARY KEY,
    custom_filter_id bigint NOT NULL,
    keyword text NOT NULL DEFAULT '',
    whole_word boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT fk_custom_filter_keywords_filter FOREIGN KEY (custom_filter_id)
        REFERENCES custom_filters(id) ON DELETE CASCADE
);

CREATE INDEX index_custom_filter_keywords_on_custom_filter_id
    ON custom_filter_keywords (custom_filter_id);

CREATE TABLE custom_filter_statuses (
    id bigserial PRIMARY KEY,
    custom_filter_id bigint NOT NULL,
    status_id bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT fk_custom_filter_statuses_filter FOREIGN KEY (custom_filter_id)
        REFERENCES custom_filters(id) ON DELETE CASCADE,
    CONSTRAINT fk_custom_filter_statuses_status FOREIGN KEY (status_id)
        REFERENCES statuses(id) ON DELETE CASCADE
);

CREATE INDEX index_custom_filter_statuses_on_custom_filter_id
    ON custom_filter_statuses (custom_filter_id);

CREATE UNIQUE INDEX index_custom_filter_statuses_on_status_id_and_custom_filter_id
    ON custom_filter_statuses (status_id, custom_filter_id);

-- +goose Down

DROP TABLE IF EXISTS custom_filter_statuses CASCADE;
DROP TABLE IF EXISTS custom_filter_keywords CASCADE;
DROP TABLE IF EXISTS custom_filters CASCADE;
