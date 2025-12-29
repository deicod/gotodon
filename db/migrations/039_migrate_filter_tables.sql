-- Migration 039: Migrate Legacy Filters to Custom Filters
-- Moves data from filters/filter_keywords/filter_statuses into custom_filters tables and drops legacy tables.

-- +goose Up

INSERT INTO custom_filters (id, account_id, phrase, context, action, expires_at, created_at, updated_at)
SELECT id, account_id, title, context, action, expires_at, created_at, updated_at
FROM filters
ON CONFLICT (id) DO NOTHING;

SELECT setval('custom_filters_id_seq', GREATEST((SELECT COALESCE(MAX(id), 0) FROM custom_filters), 1));

INSERT INTO custom_filter_keywords (id, custom_filter_id, keyword, whole_word, created_at, updated_at)
SELECT id, filter_id, keyword, whole_word, created_at, updated_at
FROM filter_keywords
ON CONFLICT (id) DO NOTHING;

SELECT setval('custom_filter_keywords_id_seq', GREATEST((SELECT COALESCE(MAX(id), 0) FROM custom_filter_keywords), 1));

INSERT INTO custom_filter_statuses (id, custom_filter_id, status_id, created_at, updated_at)
SELECT id, filter_id, status_id, created_at, updated_at
FROM filter_statuses
ON CONFLICT (id) DO NOTHING;

SELECT setval('custom_filter_statuses_id_seq', GREATEST((SELECT COALESCE(MAX(id), 0) FROM custom_filter_statuses), 1));

DROP TABLE IF EXISTS filter_statuses CASCADE;
DROP TABLE IF EXISTS filter_keywords CASCADE;
DROP TABLE IF EXISTS filters CASCADE;

-- +goose Down

CREATE TABLE filters (
    id bigserial PRIMARY KEY,
    account_id bigint NOT NULL,
    title varchar(255) NOT NULL DEFAULT '',
    context varchar(255)[] NOT NULL DEFAULT '{}',
    action integer NOT NULL DEFAULT 0,
    expires_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT fk_filters_account FOREIGN KEY (account_id)
        REFERENCES accounts(id) ON DELETE CASCADE
);

CREATE INDEX index_filters_on_account_id ON filters (account_id);

CREATE TABLE filter_keywords (
    id bigserial PRIMARY KEY,
    filter_id bigint NOT NULL,
    keyword varchar(255) NOT NULL DEFAULT '',
    whole_word boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT fk_filter_keywords_filter FOREIGN KEY (filter_id)
        REFERENCES filters(id) ON DELETE CASCADE
);

CREATE INDEX index_filter_keywords_on_filter_id ON filter_keywords (filter_id);

CREATE TABLE filter_statuses (
    id bigserial PRIMARY KEY,
    filter_id bigint NOT NULL,
    status_id bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT fk_filter_statuses_filter FOREIGN KEY (filter_id)
        REFERENCES filters(id) ON DELETE CASCADE,
    CONSTRAINT fk_filter_statuses_status FOREIGN KEY (status_id)
        REFERENCES statuses(id) ON DELETE CASCADE
);

CREATE INDEX index_filter_statuses_on_filter_id ON filter_statuses (filter_id);
CREATE UNIQUE INDEX index_filter_statuses_on_filter_status ON filter_statuses (filter_id, status_id);

INSERT INTO filters (id, account_id, title, context, action, expires_at, created_at, updated_at)
SELECT id, account_id, phrase, context, action, expires_at, created_at, updated_at
FROM custom_filters
ON CONFLICT (id) DO NOTHING;

INSERT INTO filter_keywords (id, filter_id, keyword, whole_word, created_at, updated_at)
SELECT id, custom_filter_id, keyword, whole_word, created_at, updated_at
FROM custom_filter_keywords
ON CONFLICT (id) DO NOTHING;

INSERT INTO filter_statuses (id, filter_id, status_id, created_at, updated_at)
SELECT id, custom_filter_id, status_id, created_at, updated_at
FROM custom_filter_statuses
ON CONFLICT (id) DO NOTHING;
