-- Migration 002: Core Content Tables
-- Adds status_edits, quotes, and preview_cards tables for Mastodon v4.5.3 compatibility
-- Based on Mastodon v4.5.3 schema

-- +goose Up

-- ============================================================================
-- Status Edits (Post edit history)
-- ============================================================================

CREATE TABLE status_edits (
    id bigserial PRIMARY KEY,
    status_id bigint NOT NULL,
    account_id bigint,
    text text NOT NULL DEFAULT '',
    spoiler_text text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    ordered_media_attachment_ids bigint[],
    media_descriptions text[],
    poll_options varchar(255)[],
    sensitive boolean,
    quote_id bigint,
    
    CONSTRAINT fk_status_edits_status FOREIGN KEY (status_id) 
        REFERENCES statuses(id) ON DELETE CASCADE,
    CONSTRAINT fk_status_edits_account FOREIGN KEY (account_id) 
        REFERENCES accounts(id) ON DELETE SET NULL
);

CREATE INDEX index_status_edits_on_status_id ON status_edits (status_id);
CREATE INDEX index_status_edits_on_account_id ON status_edits (account_id);

-- ============================================================================
-- Quotes (Quote posts - v4.5+ feature)
-- ============================================================================

CREATE SEQUENCE quotes_id_seq;
CREATE TABLE quotes (
    id bigint PRIMARY KEY DEFAULT timestamp_id('quotes'),
    account_id bigint NOT NULL,
    status_id bigint NOT NULL,
    quoted_status_id bigint,
    quoted_account_id bigint,
    state integer NOT NULL DEFAULT 0,
    approval_uri varchar(255),
    activity_uri varchar(255),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    legacy boolean NOT NULL DEFAULT false,
    
    CONSTRAINT fk_quotes_account FOREIGN KEY (account_id) 
        REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT fk_quotes_status FOREIGN KEY (status_id) 
        REFERENCES statuses(id) ON DELETE CASCADE,
    CONSTRAINT fk_quotes_quoted_status FOREIGN KEY (quoted_status_id) 
        REFERENCES statuses(id) ON DELETE SET NULL,
    CONSTRAINT fk_quotes_quoted_account FOREIGN KEY (quoted_account_id) 
        REFERENCES accounts(id) ON DELETE SET NULL
);

CREATE INDEX index_quotes_on_account_id_and_quoted_account_id_and_id 
    ON quotes (account_id, quoted_account_id, id);
CREATE UNIQUE INDEX index_quotes_on_activity_uri 
    ON quotes (activity_uri) WHERE activity_uri IS NOT NULL;
CREATE INDEX index_quotes_on_approval_uri 
    ON quotes (approval_uri) WHERE approval_uri IS NOT NULL;
CREATE INDEX index_quotes_on_quoted_account_id ON quotes (quoted_account_id);
CREATE INDEX index_quotes_on_quoted_status_id_and_id ON quotes (quoted_status_id, id);
CREATE UNIQUE INDEX index_quotes_on_status_id ON quotes (status_id);

-- ============================================================================
-- Preview Card Providers
-- ============================================================================

CREATE TABLE preview_card_providers (
    id bigserial PRIMARY KEY,
    domain varchar(255) NOT NULL DEFAULT '',
    icon_file_name varchar(255),
    icon_content_type varchar(255),
    icon_file_size bigint,
    icon_updated_at timestamptz,
    trendable boolean,
    reviewed_at timestamptz,
    requested_review_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX index_preview_card_providers_on_domain 
    ON preview_card_providers (domain);

-- ============================================================================
-- Preview Cards (Link previews / Open Graph cards)
-- ============================================================================

CREATE TABLE preview_cards (
    id bigserial PRIMARY KEY,
    url varchar(2048) NOT NULL DEFAULT '',
    title varchar(255) NOT NULL DEFAULT '',
    description text NOT NULL DEFAULT '',
    image_file_name varchar(255),
    image_content_type varchar(255),
    image_file_size integer,
    image_updated_at timestamptz,
    type integer NOT NULL DEFAULT 0, -- 0: link, 1: photo, 2: video, 3: rich
    html text NOT NULL DEFAULT '',
    author_name varchar(255) NOT NULL DEFAULT '',
    author_url varchar(255) NOT NULL DEFAULT '',
    provider_name varchar(255) NOT NULL DEFAULT '',
    provider_url varchar(255) NOT NULL DEFAULT '',
    width integer NOT NULL DEFAULT 0,
    height integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    embed_url varchar(255) NOT NULL DEFAULT '',
    image_storage_schema_version integer,
    blurhash varchar(255),
    language varchar(255),
    max_score float,
    max_score_at timestamptz,
    trendable boolean,
    link_type integer,
    published_at timestamptz,
    image_description varchar(255) NOT NULL DEFAULT '',
    author_account_id bigint,
    
    CONSTRAINT fk_preview_cards_author_account FOREIGN KEY (author_account_id)
        REFERENCES accounts(id) ON DELETE SET NULL
);

CREATE UNIQUE INDEX index_preview_cards_on_url ON preview_cards (url);
CREATE INDEX index_preview_cards_on_author_account_id 
    ON preview_cards (author_account_id) WHERE author_account_id IS NOT NULL;

-- ============================================================================
-- Preview Cards <-> Statuses join table
-- ============================================================================

CREATE TABLE preview_cards_statuses (
    preview_card_id bigint NOT NULL,
    status_id bigint NOT NULL,
    url varchar(2048),
    PRIMARY KEY (status_id, preview_card_id),
    
    CONSTRAINT fk_preview_cards_statuses_card FOREIGN KEY (preview_card_id) 
        REFERENCES preview_cards(id) ON DELETE CASCADE,
    CONSTRAINT fk_preview_cards_statuses_status FOREIGN KEY (status_id) 
        REFERENCES statuses(id) ON DELETE CASCADE
);

-- ============================================================================
-- Preview Card Trends
-- ============================================================================

CREATE TABLE preview_card_trends (
    id bigserial PRIMARY KEY,
    preview_card_id bigint NOT NULL,
    score float NOT NULL DEFAULT 0.0,
    rank integer NOT NULL DEFAULT 0,
    allowed boolean NOT NULL DEFAULT false,
    language varchar(255),
    
    CONSTRAINT fk_preview_card_trends_card FOREIGN KEY (preview_card_id) 
        REFERENCES preview_cards(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX index_preview_card_trends_on_preview_card_id 
    ON preview_card_trends (preview_card_id);

-- ============================================================================
-- Add quotes_count to status_stats (already exists, needs column)
-- ============================================================================

ALTER TABLE status_stats ADD COLUMN IF NOT EXISTS quotes_count bigint NOT NULL DEFAULT 0;
ALTER TABLE status_stats ADD COLUMN IF NOT EXISTS untrusted_favourites_count bigint;
ALTER TABLE status_stats ADD COLUMN IF NOT EXISTS untrusted_reblogs_count bigint;

-- ============================================================================
-- Add quote_id reference to status_edits (for edits that change quotes)
-- ============================================================================

-- Note: quote_id column already added above in status_edits table
-- Add foreign key for it:
ALTER TABLE status_edits ADD CONSTRAINT fk_status_edits_quote
    FOREIGN KEY (quote_id) REFERENCES quotes(id) ON DELETE SET NULL;

-- +goose Down

ALTER TABLE status_edits DROP CONSTRAINT IF EXISTS fk_status_edits_quote;
ALTER TABLE status_stats DROP COLUMN IF EXISTS quotes_count;
ALTER TABLE status_stats DROP COLUMN IF EXISTS untrusted_favourites_count;
ALTER TABLE status_stats DROP COLUMN IF EXISTS untrusted_reblogs_count;

DROP TABLE IF EXISTS preview_card_trends CASCADE;
DROP TABLE IF EXISTS preview_cards_statuses CASCADE;
DROP TABLE IF EXISTS preview_cards CASCADE;
DROP TABLE IF EXISTS preview_card_providers CASCADE;
DROP TABLE IF EXISTS quotes CASCADE;
DROP SEQUENCE IF EXISTS quotes_id_seq;
DROP TABLE IF EXISTS status_edits CASCADE;
