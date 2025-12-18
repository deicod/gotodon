-- Migration 004: Tag and Trends Tables
-- Adds tag_follows, tag_trends, and status_trends tables
-- Based on Mastodon v4.5.3 schema

-- +goose Up

-- ============================================================================
-- Tag Follows (allow users to follow hashtags)
-- ============================================================================

CREATE TABLE tag_follows (
    id bigserial PRIMARY KEY,
    tag_id bigint NOT NULL,
    account_id bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    
    CONSTRAINT fk_tag_follows_tag FOREIGN KEY (tag_id) 
        REFERENCES tags(id) ON DELETE CASCADE,
    CONSTRAINT fk_tag_follows_account FOREIGN KEY (account_id) 
        REFERENCES accounts(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX index_tag_follows_on_account_id_and_tag_id 
    ON tag_follows (account_id, tag_id);
CREATE INDEX index_tag_follows_on_tag_id 
    ON tag_follows (tag_id);

-- ============================================================================
-- Tag Trends (trending hashtags data)
-- ============================================================================

CREATE TABLE tag_trends (
    id bigserial PRIMARY KEY,
    tag_id bigint NOT NULL,
    score float NOT NULL DEFAULT 0.0,
    rank integer NOT NULL DEFAULT 0,
    allowed boolean NOT NULL DEFAULT false,
    language varchar(255) NOT NULL DEFAULT '',
    
    CONSTRAINT fk_tag_trends_tag FOREIGN KEY (tag_id) 
        REFERENCES tags(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX index_tag_trends_on_tag_id_and_language 
    ON tag_trends (tag_id, language);

-- ============================================================================
-- Status Trends (trending posts data)
-- ============================================================================

CREATE TABLE status_trends (
    id bigserial PRIMARY KEY,
    status_id bigint NOT NULL,
    account_id bigint NOT NULL,
    score float NOT NULL DEFAULT 0.0,
    rank integer NOT NULL DEFAULT 0,
    allowed boolean NOT NULL DEFAULT false,
    language varchar(255),
    
    CONSTRAINT fk_status_trends_status FOREIGN KEY (status_id) 
        REFERENCES statuses(id) ON DELETE CASCADE,
    CONSTRAINT fk_status_trends_account FOREIGN KEY (account_id) 
        REFERENCES accounts(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX index_status_trends_on_status_id 
    ON status_trends (status_id);
CREATE INDEX index_status_trends_on_account_id 
    ON status_trends (account_id);

-- ============================================================================
-- Update tags table with missing columns for trending/moderation
-- ============================================================================

ALTER TABLE tags ADD COLUMN IF NOT EXISTS usable boolean;
ALTER TABLE tags ADD COLUMN IF NOT EXISTS trendable boolean;
ALTER TABLE tags ADD COLUMN IF NOT EXISTS listable boolean;
ALTER TABLE tags ADD COLUMN IF NOT EXISTS reviewed_at timestamptz;
ALTER TABLE tags ADD COLUMN IF NOT EXISTS requested_review_at timestamptz;
ALTER TABLE tags ADD COLUMN IF NOT EXISTS last_status_at timestamptz;
ALTER TABLE tags ADD COLUMN IF NOT EXISTS max_score float;
ALTER TABLE tags ADD COLUMN IF NOT EXISTS max_score_at timestamptz;
ALTER TABLE tags ADD COLUMN IF NOT EXISTS display_name varchar(255);

-- +goose Down

ALTER TABLE tags DROP COLUMN IF EXISTS display_name;
ALTER TABLE tags DROP COLUMN IF EXISTS max_score_at;
ALTER TABLE tags DROP COLUMN IF EXISTS max_score;
ALTER TABLE tags DROP COLUMN IF EXISTS last_status_at;
ALTER TABLE tags DROP COLUMN IF EXISTS requested_review_at;
ALTER TABLE tags DROP COLUMN IF EXISTS reviewed_at;
ALTER TABLE tags DROP COLUMN IF EXISTS listable;
ALTER TABLE tags DROP COLUMN IF EXISTS trendable;
ALTER TABLE tags DROP COLUMN IF EXISTS usable;

DROP TABLE IF EXISTS status_trends CASCADE;
DROP TABLE IF EXISTS tag_trends CASCADE;
DROP TABLE IF EXISTS tag_follows CASCADE;
