-- Migration 005: Account Feature Tables
-- Adds account_notes, account_aliases, and account_migrations tables
-- Based on Mastodon v4.5.3 schema

-- +goose Up

-- ============================================================================
-- Account Notes (private notes about other users)
-- ============================================================================

CREATE TABLE account_notes (
    id bigserial PRIMARY KEY,
    account_id bigint NOT NULL,
    target_account_id bigint NOT NULL,
    comment text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    
    CONSTRAINT fk_account_notes_account FOREIGN KEY (account_id) 
        REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT fk_account_notes_target_account FOREIGN KEY (target_account_id) 
        REFERENCES accounts(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX index_account_notes_on_account_id_and_target_account_id 
    ON account_notes (account_id, target_account_id);
CREATE INDEX index_account_notes_on_target_account_id 
    ON account_notes (target_account_id);

-- ============================================================================
-- Account Aliases (for account migration - "I also go by...")
-- ============================================================================

CREATE TABLE account_aliases (
    id bigserial PRIMARY KEY,
    account_id bigint NOT NULL,
    acct varchar(255) NOT NULL DEFAULT '',
    uri varchar(2048) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    
    CONSTRAINT fk_account_aliases_account FOREIGN KEY (account_id) 
        REFERENCES accounts(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX index_account_aliases_on_account_id_and_uri 
    ON account_aliases (account_id, uri);

-- ============================================================================
-- Account Migrations (tracking account moves)
-- ============================================================================

CREATE TABLE account_migrations (
    id bigserial PRIMARY KEY,
    account_id bigint,
    acct varchar(255) NOT NULL DEFAULT '',
    followers_count bigint NOT NULL DEFAULT 0,
    target_account_id bigint,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    
    CONSTRAINT fk_account_migrations_account FOREIGN KEY (account_id) 
        REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT fk_account_migrations_target_account FOREIGN KEY (target_account_id) 
        REFERENCES accounts(id) ON DELETE SET NULL
);

CREATE INDEX index_account_migrations_on_account_id 
    ON account_migrations (account_id);
CREATE INDEX index_account_migrations_on_target_account_id 
    ON account_migrations (target_account_id) WHERE target_account_id IS NOT NULL;

-- +goose Down

DROP TABLE IF EXISTS account_migrations CASCADE;
DROP TABLE IF EXISTS account_aliases CASCADE;
DROP TABLE IF EXISTS account_notes CASCADE;
