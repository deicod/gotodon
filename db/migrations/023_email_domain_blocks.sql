-- Migration 023: Email Domain Blocks
-- Adds email_domain_blocks and unavailable_domains tables.

-- +goose Up

CREATE TABLE email_domain_blocks (
    id bigserial PRIMARY KEY,
    domain varchar(255) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    parent_id bigint,
    allow_with_approval boolean NOT NULL DEFAULT false,

    CONSTRAINT fk_email_domain_blocks_parent FOREIGN KEY (parent_id)
        REFERENCES email_domain_blocks(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX index_email_domain_blocks_on_domain
    ON email_domain_blocks (domain);

CREATE TABLE unavailable_domains (
    id bigserial PRIMARY KEY,
    domain varchar(255) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX index_unavailable_domains_on_domain
    ON unavailable_domains (domain);

-- +goose Down

DROP TABLE IF EXISTS unavailable_domains CASCADE;
DROP TABLE IF EXISTS email_domain_blocks CASCADE;
