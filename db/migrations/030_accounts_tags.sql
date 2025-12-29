-- Migration 030: Accounts Tags
-- Adds accounts_tags join table.

-- +goose Up

CREATE TABLE accounts_tags (
    account_id bigint NOT NULL,
    tag_id bigint NOT NULL,
    PRIMARY KEY (tag_id, account_id)
);

CREATE INDEX index_accounts_tags_on_account_id_and_tag_id
    ON accounts_tags (account_id, tag_id);

-- +goose Down

DROP TABLE IF EXISTS accounts_tags CASCADE;
