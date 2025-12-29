-- Migration 031: Align Accounts
-- Adds id_scheme, search index, and moved_to_account_id index.

-- +goose Up

ALTER TABLE accounts
    ADD COLUMN id_scheme integer DEFAULT 1;

CREATE INDEX search_index
    ON accounts
    USING gin (
        (
            (
                setweight(to_tsvector('simple'::regconfig, (display_name)::text), 'A'::"char")
                || setweight(to_tsvector('simple'::regconfig, (username)::text), 'B'::"char")
            )
            || setweight(
                to_tsvector('simple'::regconfig, (COALESCE(domain, ''::character varying))::text),
                'C'::"char"
            )
        )
    );

CREATE INDEX index_accounts_on_moved_to_account_id
    ON accounts (moved_to_account_id)
    WHERE moved_to_account_id IS NOT NULL;

-- +goose Down

DROP INDEX IF EXISTS index_accounts_on_moved_to_account_id;
DROP INDEX IF EXISTS search_index;

ALTER TABLE accounts
    DROP COLUMN IF EXISTS id_scheme;
