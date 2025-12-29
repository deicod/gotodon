-- Migration 014: Account Deletion Requests
-- Adds account_deletion_requests table.

-- +goose Up

CREATE TABLE account_deletion_requests (
    id bigserial PRIMARY KEY,
    account_id bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT fk_account_deletion_requests_account FOREIGN KEY (account_id)
        REFERENCES accounts(id) ON DELETE CASCADE
);

CREATE INDEX index_account_deletion_requests_on_account_id
    ON account_deletion_requests (account_id);

-- +goose Down

DROP TABLE IF EXISTS account_deletion_requests CASCADE;
