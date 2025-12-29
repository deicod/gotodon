-- Migration 013: Account Conversations
-- Adds account_conversations table for per-account conversation state.

-- +goose Up

CREATE TABLE account_conversations (
    id bigserial PRIMARY KEY,
    account_id bigint NOT NULL,
    conversation_id bigint NOT NULL,
    participant_account_ids bigint[] NOT NULL DEFAULT '{}',
    status_ids bigint[] NOT NULL DEFAULT '{}',
    last_status_id bigint,
    lock_version integer NOT NULL DEFAULT 0,
    unread boolean NOT NULL DEFAULT false,

    CONSTRAINT fk_account_conversations_account FOREIGN KEY (account_id)
        REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT fk_account_conversations_conversation FOREIGN KEY (conversation_id)
        REFERENCES conversations(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX index_unique_conversations
    ON account_conversations (account_id, conversation_id, participant_account_ids);

CREATE INDEX index_account_conversations_on_conversation_id
    ON account_conversations (conversation_id);

-- +goose Down

DROP TABLE IF EXISTS account_conversations CASCADE;
