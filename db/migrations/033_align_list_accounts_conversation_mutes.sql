-- Migration 033: Align List Accounts and Conversation Mutes
-- Removes extra timestamps, adds follow_request_id, and aligns indexes.

-- +goose Up

ALTER TABLE list_accounts
    ADD COLUMN follow_request_id bigint;

ALTER TABLE list_accounts
    ADD CONSTRAINT fk_list_accounts_follow_request FOREIGN KEY (follow_request_id)
        REFERENCES follow_requests(id) ON DELETE CASCADE;

ALTER TABLE list_accounts
    DROP COLUMN created_at,
    DROP COLUMN updated_at;

DROP INDEX IF EXISTS index_list_accounts_on_list_and_account;
DROP INDEX IF EXISTS index_list_accounts_on_account_id;
DROP INDEX IF EXISTS index_list_accounts_on_follow_id;

CREATE UNIQUE INDEX index_list_accounts_on_account_id_and_list_id
    ON list_accounts (account_id, list_id);

CREATE INDEX index_list_accounts_on_list_id_and_account_id
    ON list_accounts (list_id, account_id);

CREATE INDEX index_list_accounts_on_follow_id
    ON list_accounts (follow_id)
    WHERE follow_id IS NOT NULL;

CREATE INDEX index_list_accounts_on_follow_request_id
    ON list_accounts (follow_request_id)
    WHERE follow_request_id IS NOT NULL;

ALTER TABLE conversation_mutes
    DROP COLUMN created_at,
    DROP COLUMN updated_at;

DROP INDEX IF EXISTS index_conversation_mutes_on_account_conversation;

CREATE UNIQUE INDEX index_conversation_mutes_on_account_id_and_conversation_id
    ON conversation_mutes (account_id, conversation_id);

-- +goose Down

DROP INDEX IF EXISTS index_conversation_mutes_on_account_id_and_conversation_id;

ALTER TABLE conversation_mutes
    ADD COLUMN created_at timestamptz NOT NULL DEFAULT now(),
    ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();

CREATE UNIQUE INDEX index_conversation_mutes_on_account_conversation
    ON conversation_mutes (account_id, conversation_id);

DROP INDEX IF EXISTS index_list_accounts_on_follow_request_id;
DROP INDEX IF EXISTS index_list_accounts_on_follow_id;
DROP INDEX IF EXISTS index_list_accounts_on_list_id_and_account_id;
DROP INDEX IF EXISTS index_list_accounts_on_account_id_and_list_id;

CREATE UNIQUE INDEX index_list_accounts_on_list_and_account
    ON list_accounts (list_id, account_id);

CREATE INDEX index_list_accounts_on_account_id
    ON list_accounts (account_id);

CREATE INDEX index_list_accounts_on_follow_id
    ON list_accounts (follow_id);

ALTER TABLE list_accounts
    ADD COLUMN created_at timestamptz NOT NULL DEFAULT now(),
    ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();

ALTER TABLE list_accounts
    DROP CONSTRAINT IF EXISTS fk_list_accounts_follow_request;

ALTER TABLE list_accounts
    DROP COLUMN IF EXISTS follow_request_id;
