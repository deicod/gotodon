-- Migration 006: Admin and Moderation Tables
-- Adds rules, rule_translations, domain_allows, and tombstones tables
-- Based on Mastodon v4.5.3 schema

-- +goose Up

-- ============================================================================
-- Rules (server rules displayed to users)
-- ============================================================================

CREATE TABLE rules (
    id bigserial PRIMARY KEY,
    priority integer NOT NULL DEFAULT 0,
    deleted_at timestamptz,
    text text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    hint text NOT NULL DEFAULT ''
);

-- ============================================================================
-- Rule Translations (i18n for server rules)
-- ============================================================================

CREATE TABLE rule_translations (
    id bigserial PRIMARY KEY,
    text text NOT NULL DEFAULT '',
    hint text NOT NULL DEFAULT '',
    language varchar(255) NOT NULL,
    rule_id bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    
    CONSTRAINT fk_rule_translations_rule FOREIGN KEY (rule_id) 
        REFERENCES rules(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX index_rule_translations_on_rule_id_and_language 
    ON rule_translations (rule_id, language);

-- ============================================================================
-- Domain Allows (federation allow list - opposite of domain blocks)
-- ============================================================================

CREATE TABLE domain_allows (
    id bigserial PRIMARY KEY,
    domain varchar(255) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX index_domain_allows_on_domain ON domain_allows (domain);

-- ============================================================================
-- Tombstones (tracking deleted content for ActivityPub)
-- ============================================================================

CREATE TABLE tombstones (
    id bigserial PRIMARY KEY,
    account_id bigint NOT NULL,
    uri varchar(2048) NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    by_moderator boolean,
    
    CONSTRAINT fk_tombstones_account FOREIGN KEY (account_id) 
        REFERENCES accounts(id) ON DELETE CASCADE
);

CREATE INDEX index_tombstones_on_account_id ON tombstones (account_id);
CREATE INDEX index_tombstones_on_uri ON tombstones (uri);

-- +goose Down

DROP TABLE IF EXISTS tombstones CASCADE;
DROP TABLE IF EXISTS domain_allows CASCADE;
DROP TABLE IF EXISTS rule_translations CASCADE;
DROP TABLE IF EXISTS rules CASCADE;
