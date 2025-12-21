-- Migration 011: Canonical Email Blocks
-- Adds canonical_email_blocks table
-- Based on Mastodon v4.5.3 schema

-- +goose Up

CREATE TABLE canonical_email_blocks (
    id bigserial PRIMARY KEY,
    canonical_email_hash varchar(255) NOT NULL DEFAULT '',
    reference_account_id bigint,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT fk_canonical_email_blocks_reference_account
        FOREIGN KEY (reference_account_id)
        REFERENCES accounts(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX index_canonical_email_blocks_on_canonical_email_hash
    ON canonical_email_blocks (canonical_email_hash);

CREATE INDEX index_canonical_email_blocks_on_reference_account_id
    ON canonical_email_blocks (reference_account_id);

-- +goose Down

DROP TABLE IF EXISTS canonical_email_blocks CASCADE;
