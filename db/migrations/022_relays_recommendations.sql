-- Migration 022: Relays and Follow Recommendations
-- Adds relays, follow_recommendation_mutes, and follow_recommendation_suppressions tables.

-- +goose Up

CREATE TABLE relays (
    id bigserial PRIMARY KEY,
    inbox_url varchar(255) NOT NULL DEFAULT '',
    follow_activity_id varchar(255),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    state integer NOT NULL DEFAULT 0
);

CREATE TABLE follow_recommendation_mutes (
    id bigserial PRIMARY KEY,
    account_id bigint NOT NULL,
    target_account_id bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT fk_follow_recommendation_mutes_account FOREIGN KEY (account_id)
        REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT fk_follow_recommendation_mutes_target FOREIGN KEY (target_account_id)
        REFERENCES accounts(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX idx_on_account_id_target_account_id_a8c8ddf44e
    ON follow_recommendation_mutes (account_id, target_account_id);

CREATE INDEX index_follow_recommendation_mutes_on_target_account_id
    ON follow_recommendation_mutes (target_account_id);

CREATE TABLE follow_recommendation_suppressions (
    id bigserial PRIMARY KEY,
    account_id bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT fk_follow_recommendation_suppressions_account FOREIGN KEY (account_id)
        REFERENCES accounts(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX index_follow_recommendation_suppressions_on_account_id
    ON follow_recommendation_suppressions (account_id);

-- +goose Down

DROP TABLE IF EXISTS follow_recommendation_suppressions CASCADE;
DROP TABLE IF EXISTS follow_recommendation_mutes CASCADE;
DROP TABLE IF EXISTS relays CASCADE;
