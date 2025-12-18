-- Gotodon Complete Initial Schema
-- Based on Mastodon v4.5.2 database schema
-- Consolidated migration containing all tables

-- +goose Up
-- Enable required extensions
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- Snowflake ID generation function (Mastodon-compatible)
CREATE OR REPLACE FUNCTION timestamp_id(table_name text) RETURNS bigint AS $$
DECLARE
    epoch bigint := 1514764800000; -- 2018-01-01 00:00:00 UTC in milliseconds
    seq_id bigint;
    now_millis bigint;
    result bigint;
BEGIN
    SELECT nextval(table_name || '_id_seq') INTO seq_id;
    SELECT FLOOR(EXTRACT(EPOCH FROM clock_timestamp()) * 1000) INTO now_millis;
    result := (now_millis - epoch) << 16;
    result := result | (seq_id & 65535);
    RETURN result;
END;
$$ LANGUAGE plpgsql;

-- ============================================================================
-- Core Tables
-- ============================================================================

-- User Roles
CREATE TABLE user_roles (
    id bigserial PRIMARY KEY,
    name varchar(255) NOT NULL DEFAULT '',
    color varchar(255) NOT NULL DEFAULT '',
    position integer NOT NULL DEFAULT 0,
    permissions bigint NOT NULL DEFAULT 0,
    highlighted boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

INSERT INTO user_roles (name, permissions, position) VALUES 
    ('Owner', -1, 1000),
    ('Admin', 983103, 100),
    ('Moderator', 9423, 10);

-- Accounts table (core entity)
CREATE SEQUENCE accounts_id_seq;
CREATE TABLE accounts (
    id bigint PRIMARY KEY DEFAULT timestamp_id('accounts'),
    username varchar(255) NOT NULL DEFAULT '',
    domain varchar(255),
    private_key text,
    public_key text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    note text NOT NULL DEFAULT '',
    display_name varchar(255) NOT NULL DEFAULT '',
    uri varchar(255) NOT NULL DEFAULT '',
    url varchar(255),
    avatar_file_name varchar(255),
    avatar_content_type varchar(255),
    avatar_file_size integer,
    avatar_updated_at timestamptz,
    header_file_name varchar(255),
    header_content_type varchar(255),
    header_file_size integer,
    header_updated_at timestamptz,
    avatar_remote_url varchar(2048),
    locked boolean NOT NULL DEFAULT false,
    header_remote_url varchar(2048) NOT NULL DEFAULT '',
    last_webfingered_at timestamptz,
    inbox_url varchar(2048) NOT NULL DEFAULT '',
    outbox_url varchar(2048) NOT NULL DEFAULT '',
    shared_inbox_url varchar(2048) NOT NULL DEFAULT '',
    followers_url varchar(2048) NOT NULL DEFAULT '',
    following_url varchar(2048) NOT NULL DEFAULT '',
    protocol integer NOT NULL DEFAULT 1, -- 0: ostatus, 1: activitypub
    memorial boolean NOT NULL DEFAULT false,
    moved_to_account_id bigint,
    featured_collection_url varchar(2048),
    fields jsonb,
    actor_type varchar(255),
    discoverable boolean,
    also_known_as varchar(255)[],
    silenced_at timestamptz,
    suspended_at timestamptz,
    hide_collections boolean,
    avatar_storage_schema_version integer,
    header_storage_schema_version integer,
    suspension_origin integer,
    sensitized_at timestamptz,
    trendable boolean,
    reviewed_at timestamptz,
    requested_review_at timestamptz,
    indexable boolean NOT NULL DEFAULT false,
    attribution_domains varchar(255)[] DEFAULT '{}',
    
    CONSTRAINT fk_accounts_moved_to FOREIGN KEY (moved_to_account_id) 
        REFERENCES accounts(id) ON DELETE SET NULL
);

CREATE UNIQUE INDEX index_accounts_on_username_and_domain_lower 
    ON accounts (lower(username), COALESCE(lower(domain), ''));
CREATE INDEX index_accounts_on_uri ON accounts (uri);
CREATE INDEX index_accounts_on_url ON accounts (url text_pattern_ops) WHERE url IS NOT NULL;
CREATE INDEX index_accounts_on_domain_and_id ON accounts (domain, id);

-- Users table (authentication)
CREATE TABLE users (
    id bigserial PRIMARY KEY,
    email varchar(255) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    encrypted_password varchar(255) NOT NULL DEFAULT '',
    reset_password_token varchar(255),
    reset_password_sent_at timestamptz,
    sign_in_count integer NOT NULL DEFAULT 0,
    current_sign_in_at timestamptz,
    last_sign_in_at timestamptz,
    confirmation_token varchar(255),
    confirmed_at timestamptz,
    confirmation_sent_at timestamptz,
    unconfirmed_email varchar(255),
    locale varchar(255),
    consumed_timestep integer,
    otp_required_for_login boolean NOT NULL DEFAULT false,
    last_emailed_at timestamptz,
    otp_backup_codes varchar(255)[],
    account_id bigint NOT NULL,
    disabled boolean NOT NULL DEFAULT false,
    invite_id bigint,
    chosen_languages varchar(255)[],
    created_by_application_id bigint,
    approved boolean NOT NULL DEFAULT true,
    sign_in_token varchar(255),
    sign_in_token_sent_at timestamptz,
    webauthn_id varchar(255),
    sign_up_ip inet,
    skip_sign_in_token boolean,
    role_id bigint,
    settings text,
    time_zone varchar(255),
    otp_secret varchar(255),
    
    CONSTRAINT fk_users_account FOREIGN KEY (account_id) 
        REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT fk_users_role FOREIGN KEY (role_id) 
        REFERENCES user_roles(id) ON DELETE SET NULL
);

CREATE UNIQUE INDEX index_users_on_email ON users (email);
CREATE UNIQUE INDEX index_users_on_confirmation_token ON users (confirmation_token);
CREATE UNIQUE INDEX index_users_on_reset_password_token ON users (reset_password_token) 
    WHERE reset_password_token IS NOT NULL;
CREATE INDEX index_users_on_account_id ON users (account_id);

-- Account Stats
CREATE TABLE account_stats (
    id bigserial PRIMARY KEY,
    account_id bigint NOT NULL,
    statuses_count bigint NOT NULL DEFAULT 0,
    following_count bigint NOT NULL DEFAULT 0,
    followers_count bigint NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    last_status_at timestamptz,
    
    CONSTRAINT fk_account_stats_account FOREIGN KEY (account_id) 
        REFERENCES accounts(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX index_account_stats_on_account_id ON account_stats (account_id);

-- ============================================================================
-- OAuth
-- ============================================================================

-- OAuth Applications (Doorkeeper compatible)
CREATE TABLE oauth_applications (
    id bigserial PRIMARY KEY,
    name varchar(255) NOT NULL,
    uid varchar(255) NOT NULL,
    secret varchar(255) NOT NULL,
    redirect_uri text NOT NULL,
    scopes varchar(255) NOT NULL DEFAULT '',
    created_at timestamptz,
    updated_at timestamptz,
    superapp boolean NOT NULL DEFAULT false,
    website varchar(255),
    owner_type varchar(255),
    owner_id bigint,
    confidential boolean NOT NULL DEFAULT true
);

CREATE UNIQUE INDEX index_oauth_applications_on_uid ON oauth_applications (uid);

-- OAuth Access Tokens
CREATE TABLE oauth_access_tokens (
    id bigserial PRIMARY KEY,
    token varchar(255) NOT NULL,
    refresh_token varchar(255),
    expires_in integer,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    scopes varchar(255),
    application_id bigint,
    resource_owner_id bigint,
    last_used_at timestamptz,
    last_used_ip inet,
    
    CONSTRAINT fk_oauth_tokens_application FOREIGN KEY (application_id) 
        REFERENCES oauth_applications(id) ON DELETE CASCADE,
    CONSTRAINT fk_oauth_tokens_user FOREIGN KEY (resource_owner_id) 
        REFERENCES users(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX index_oauth_access_tokens_on_token ON oauth_access_tokens (token);
CREATE UNIQUE INDEX index_oauth_access_tokens_on_refresh_token ON oauth_access_tokens (refresh_token) 
    WHERE refresh_token IS NOT NULL;
CREATE INDEX index_oauth_access_tokens_on_resource_owner_id ON oauth_access_tokens (resource_owner_id) 
    WHERE resource_owner_id IS NOT NULL;

-- ============================================================================
-- Statuses and Content
-- ============================================================================

-- Conversations
CREATE TABLE conversations (
    id bigserial PRIMARY KEY,
    uri varchar(255),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    parent_status_id bigint,
    parent_account_id bigint
);

CREATE UNIQUE INDEX index_conversations_on_uri ON conversations (uri text_pattern_ops) WHERE uri IS NOT NULL;

-- Statuses table (posts/toots)
CREATE SEQUENCE statuses_id_seq;
CREATE TABLE statuses (
    id bigint PRIMARY KEY DEFAULT timestamp_id('statuses'),
    uri varchar(255),
    text text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    in_reply_to_id bigint,
    reblog_of_id bigint,
    url varchar(255),
    sensitive boolean NOT NULL DEFAULT false,
    visibility integer NOT NULL DEFAULT 0, -- 0: public, 1: unlisted, 2: private, 3: direct
    spoiler_text text NOT NULL DEFAULT '',
    reply boolean NOT NULL DEFAULT false,
    language varchar(255),
    conversation_id bigint,
    local boolean,
    account_id bigint NOT NULL,
    application_id bigint,
    in_reply_to_account_id bigint,
    poll_id bigint,
    deleted_at timestamptz,
    edited_at timestamptz,
    trendable boolean,
    ordered_media_attachment_ids bigint[],
    fetched_replies_at timestamptz,
    
    CONSTRAINT fk_statuses_account FOREIGN KEY (account_id) 
        REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT fk_statuses_in_reply_to FOREIGN KEY (in_reply_to_id) 
        REFERENCES statuses(id) ON DELETE SET NULL,
    CONSTRAINT fk_statuses_reblog_of FOREIGN KEY (reblog_of_id) 
        REFERENCES statuses(id) ON DELETE CASCADE,
    CONSTRAINT fk_statuses_conversation FOREIGN KEY (conversation_id) 
        REFERENCES conversations(id) ON DELETE SET NULL,
    CONSTRAINT fk_statuses_in_reply_to_account FOREIGN KEY (in_reply_to_account_id) 
        REFERENCES accounts(id) ON DELETE SET NULL,
    CONSTRAINT fk_statuses_application FOREIGN KEY (application_id) 
        REFERENCES oauth_applications(id) ON DELETE SET NULL
);

CREATE UNIQUE INDEX index_statuses_on_uri ON statuses (uri text_pattern_ops) WHERE uri IS NOT NULL;
CREATE INDEX index_statuses_on_account_id ON statuses (account_id);
CREATE INDEX index_statuses_on_conversation_id ON statuses (conversation_id);
CREATE INDEX index_statuses_on_in_reply_to_id ON statuses (in_reply_to_id) WHERE in_reply_to_id IS NOT NULL;
CREATE INDEX index_statuses_on_reblog_of_id_and_account_id ON statuses (reblog_of_id, account_id);

-- Status Stats
CREATE TABLE status_stats (
    id bigserial PRIMARY KEY,
    status_id bigint NOT NULL,
    replies_count bigint NOT NULL DEFAULT 0,
    reblogs_count bigint NOT NULL DEFAULT 0,
    favourites_count bigint NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    
    CONSTRAINT fk_status_stats_status FOREIGN KEY (status_id) 
        REFERENCES statuses(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX index_status_stats_on_status_id ON status_stats (status_id);

-- Scheduled Statuses
CREATE SEQUENCE scheduled_statuses_id_seq;
CREATE TABLE scheduled_statuses (
    id bigint PRIMARY KEY DEFAULT timestamp_id('scheduled_statuses'),
    account_id bigint NOT NULL,
    scheduled_at timestamptz NOT NULL,
    params jsonb NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    
    CONSTRAINT fk_scheduled_statuses_account FOREIGN KEY (account_id) 
        REFERENCES accounts(id) ON DELETE CASCADE
);

CREATE INDEX index_scheduled_statuses_on_account_id ON scheduled_statuses (account_id);
CREATE INDEX index_scheduled_statuses_on_scheduled_at ON scheduled_statuses (scheduled_at);

-- Status Pins
CREATE TABLE status_pins (
    id bigserial PRIMARY KEY,
    account_id bigint NOT NULL,
    status_id bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    
    CONSTRAINT fk_status_pins_account FOREIGN KEY (account_id) 
        REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT fk_status_pins_status FOREIGN KEY (status_id) 
        REFERENCES statuses(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX index_status_pins_on_account_status ON status_pins (account_id, status_id);
CREATE INDEX index_status_pins_on_status_id ON status_pins (status_id);

-- ============================================================================
-- Media
-- ============================================================================

-- Media Attachments
CREATE SEQUENCE media_attachments_id_seq;
CREATE TABLE media_attachments (
    id bigint PRIMARY KEY DEFAULT timestamp_id('media_attachments'),
    status_id bigint,
    file_file_name varchar(255),
    file_content_type varchar(255),
    file_file_size integer,
    file_updated_at timestamptz,
    remote_url varchar(2048) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    shortcode varchar(255),
    type integer NOT NULL DEFAULT 0, -- 0: image, 1: gifv, 2: video, 3: audio, 4: unknown
    file_meta jsonb,
    account_id bigint,
    description text,
    scheduled_status_id bigint,
    blurhash varchar(255),
    processing integer,
    file_storage_schema_version integer,
    thumbnail_file_name varchar(255),
    thumbnail_content_type varchar(255),
    thumbnail_file_size integer,
    thumbnail_updated_at timestamptz,
    thumbnail_remote_url varchar(2048),
    
    CONSTRAINT fk_media_attachments_status FOREIGN KEY (status_id) 
        REFERENCES statuses(id) ON DELETE SET NULL,
    CONSTRAINT fk_media_attachments_account FOREIGN KEY (account_id) 
        REFERENCES accounts(id) ON DELETE SET NULL
);

CREATE UNIQUE INDEX index_media_attachments_on_shortcode ON media_attachments (shortcode text_pattern_ops) 
    WHERE shortcode IS NOT NULL;
CREATE INDEX index_media_attachments_on_status_id ON media_attachments (status_id);
CREATE INDEX index_media_attachments_on_account_id_and_status_id ON media_attachments (account_id, status_id DESC);

-- ============================================================================
-- Relationships
-- ============================================================================

-- Follows
CREATE TABLE follows (
    id bigserial PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    account_id bigint NOT NULL,
    target_account_id bigint NOT NULL,
    show_reblogs boolean NOT NULL DEFAULT true,
    uri varchar(255),
    notify boolean NOT NULL DEFAULT false,
    languages varchar(255)[],
    
    CONSTRAINT fk_follows_account FOREIGN KEY (account_id) 
        REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT fk_follows_target FOREIGN KEY (target_account_id) 
        REFERENCES accounts(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX index_follows_on_account_id_and_target_account_id 
    ON follows (account_id, target_account_id);
CREATE INDEX index_follows_on_target_account_id_and_account_id 
    ON follows (target_account_id, account_id);

-- Follow Requests
CREATE TABLE follow_requests (
    id bigserial PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    account_id bigint NOT NULL,
    target_account_id bigint NOT NULL,
    show_reblogs boolean NOT NULL DEFAULT true,
    uri varchar(255),
    notify boolean NOT NULL DEFAULT false,
    languages varchar(255)[],
    
    CONSTRAINT fk_follow_requests_account FOREIGN KEY (account_id) 
        REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT fk_follow_requests_target FOREIGN KEY (target_account_id) 
        REFERENCES accounts(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX index_follow_requests_on_account_id_and_target_account_id 
    ON follow_requests (account_id, target_account_id);

-- Favourites
CREATE TABLE favourites (
    id bigserial PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    account_id bigint NOT NULL,
    status_id bigint NOT NULL,
    
    CONSTRAINT fk_favourites_account FOREIGN KEY (account_id) 
        REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT fk_favourites_status FOREIGN KEY (status_id) 
        REFERENCES statuses(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX index_favourites_on_account_id_and_status_id 
    ON favourites (account_id, status_id);
CREATE INDEX index_favourites_on_status_id ON favourites (status_id);

-- Bookmarks
CREATE TABLE bookmarks (
    id bigserial PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    account_id bigint NOT NULL,
    status_id bigint NOT NULL,
    
    CONSTRAINT fk_bookmarks_account FOREIGN KEY (account_id) 
        REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT fk_bookmarks_status FOREIGN KEY (status_id) 
        REFERENCES statuses(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX index_bookmarks_on_account_id_and_status_id ON bookmarks (account_id, status_id);
CREATE INDEX index_bookmarks_on_status_id ON bookmarks (status_id);

-- Blocks
CREATE TABLE blocks (
    id bigserial PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    account_id bigint NOT NULL,
    target_account_id bigint NOT NULL,
    uri varchar(255),
    
    CONSTRAINT fk_blocks_account FOREIGN KEY (account_id) 
        REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT fk_blocks_target FOREIGN KEY (target_account_id) 
        REFERENCES accounts(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX index_blocks_on_account_id_and_target_account_id 
    ON blocks (account_id, target_account_id);
CREATE INDEX index_blocks_on_target_account_id ON blocks (target_account_id);

-- Mutes
CREATE TABLE mutes (
    id bigserial PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    hide_notifications boolean NOT NULL DEFAULT true,
    account_id bigint NOT NULL,
    target_account_id bigint NOT NULL,
    expires_at timestamptz,
    
    CONSTRAINT fk_mutes_account FOREIGN KEY (account_id) 
        REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT fk_mutes_target FOREIGN KEY (target_account_id) 
        REFERENCES accounts(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX index_mutes_on_account_id_and_target_account_id 
    ON mutes (account_id, target_account_id);
CREATE INDEX index_mutes_on_target_account_id ON mutes (target_account_id);

-- Domain Blocks (user-level)
CREATE TABLE account_domain_blocks (
    id bigserial PRIMARY KEY,
    domain varchar(255) NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    account_id bigint NOT NULL,
    
    CONSTRAINT fk_account_domain_blocks_account FOREIGN KEY (account_id) 
        REFERENCES accounts(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX index_account_domain_blocks_on_account_domain 
    ON account_domain_blocks (account_id, domain);

-- Domain Blocks (admin-level)
CREATE TABLE domain_blocks (
    id bigserial PRIMARY KEY,
    domain varchar(255) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    severity integer NOT NULL DEFAULT 0,
    reject_media boolean NOT NULL DEFAULT false,
    reject_reports boolean NOT NULL DEFAULT false,
    private_comment text,
    public_comment text,
    obfuscate boolean NOT NULL DEFAULT false
);

CREATE UNIQUE INDEX index_domain_blocks_on_domain ON domain_blocks (domain);

-- Conversation Mutes
CREATE TABLE conversation_mutes (
    id bigserial PRIMARY KEY,
    conversation_id bigint NOT NULL,
    account_id bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    
    CONSTRAINT fk_conversation_mutes_conversation FOREIGN KEY (conversation_id) 
        REFERENCES conversations(id) ON DELETE CASCADE,
    CONSTRAINT fk_conversation_mutes_account FOREIGN KEY (account_id) 
        REFERENCES accounts(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX index_conversation_mutes_on_account_conversation 
    ON conversation_mutes (account_id, conversation_id);

-- ============================================================================
-- Mentions and Tags
-- ============================================================================

-- Mentions
CREATE TABLE mentions (
    id bigserial PRIMARY KEY,
    status_id bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    account_id bigint NOT NULL,
    silent boolean NOT NULL DEFAULT false,
    
    CONSTRAINT fk_mentions_status FOREIGN KEY (status_id) 
        REFERENCES statuses(id) ON DELETE CASCADE,
    CONSTRAINT fk_mentions_account FOREIGN KEY (account_id) 
        REFERENCES accounts(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX index_mentions_on_account_id_and_status_id 
    ON mentions (account_id, status_id);
CREATE INDEX index_mentions_on_status_id ON mentions (status_id);

-- Tags (hashtags)
CREATE TABLE tags (
    id bigserial PRIMARY KEY,
    name varchar(255) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    usable boolean,
    trendable boolean,
    listable boolean,
    reviewed_at timestamptz,
    requested_review_at timestamptz,
    last_status_at timestamptz,
    max_score float,
    max_score_at timestamptz,
    display_name varchar(255)
);

CREATE UNIQUE INDEX index_tags_on_name_lower_btree ON tags (lower(name) text_pattern_ops);

-- Statuses<->Tags join table
CREATE TABLE statuses_tags (
    status_id bigint NOT NULL,
    tag_id bigint NOT NULL,
    PRIMARY KEY (tag_id, status_id),
    
    CONSTRAINT fk_statuses_tags_status FOREIGN KEY (status_id) 
        REFERENCES statuses(id) ON DELETE CASCADE,
    CONSTRAINT fk_statuses_tags_tag FOREIGN KEY (tag_id) 
        REFERENCES tags(id) ON DELETE CASCADE
);

CREATE INDEX index_statuses_tags_on_status_id ON statuses_tags (status_id);

-- ============================================================================
-- Lists
-- ============================================================================

-- Lists
CREATE TABLE lists (
    id bigserial PRIMARY KEY,
    account_id bigint NOT NULL,
    title varchar(255) NOT NULL DEFAULT '',
    replies_policy integer NOT NULL DEFAULT 0,
    exclusive boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    
    CONSTRAINT fk_lists_account FOREIGN KEY (account_id) 
        REFERENCES accounts(id) ON DELETE CASCADE
);

CREATE INDEX index_lists_on_account_id ON lists (account_id);

-- List Accounts
CREATE TABLE list_accounts (
    id bigserial PRIMARY KEY,
    list_id bigint NOT NULL,
    account_id bigint NOT NULL,
    follow_id bigint,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    
    CONSTRAINT fk_list_accounts_list FOREIGN KEY (list_id) 
        REFERENCES lists(id) ON DELETE CASCADE,
    CONSTRAINT fk_list_accounts_account FOREIGN KEY (account_id) 
        REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT fk_list_accounts_follow FOREIGN KEY (follow_id) 
        REFERENCES follows(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX index_list_accounts_on_list_and_account ON list_accounts (list_id, account_id);
CREATE INDEX index_list_accounts_on_account_id ON list_accounts (account_id);
CREATE INDEX index_list_accounts_on_follow_id ON list_accounts (follow_id);

-- ============================================================================
-- Polls
-- ============================================================================

-- Polls
CREATE TABLE polls (
    id bigserial PRIMARY KEY,
    account_id bigint,
    status_id bigint,
    expires_at timestamptz NOT NULL,
    options varchar(255)[] NOT NULL DEFAULT '{}',
    cached_tallies jsonb NOT NULL DEFAULT '{}',
    multiple boolean NOT NULL DEFAULT false,
    hide_totals boolean NOT NULL DEFAULT false,
    votes_count bigint NOT NULL DEFAULT 0,
    voters_count bigint,
    last_fetched_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    lock_version integer NOT NULL DEFAULT 0,
    
    CONSTRAINT fk_polls_account FOREIGN KEY (account_id) 
        REFERENCES accounts(id) ON DELETE SET NULL,
    CONSTRAINT fk_polls_status FOREIGN KEY (status_id)
        REFERENCES statuses(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX index_polls_on_status_id ON polls (status_id);

-- Poll Votes
CREATE TABLE poll_votes (
    id bigserial PRIMARY KEY,
    account_id bigint,
    poll_id bigint NOT NULL,
    choice integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    uri varchar(255),
    
    CONSTRAINT fk_poll_votes_account FOREIGN KEY (account_id) 
        REFERENCES accounts(id) ON DELETE SET NULL,
    CONSTRAINT fk_poll_votes_poll FOREIGN KEY (poll_id)
        REFERENCES polls(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX index_poll_votes_on_poll_account_choice ON poll_votes (poll_id, account_id, choice);
CREATE INDEX index_poll_votes_on_poll_id ON poll_votes (poll_id);

-- Add poll_id FK to statuses
ALTER TABLE statuses ADD CONSTRAINT fk_statuses_poll 
    FOREIGN KEY (poll_id) REFERENCES polls(id) ON DELETE SET NULL;

-- ============================================================================
-- Filters
-- ============================================================================

-- Filters (v2 API)
CREATE TABLE filters (
    id bigserial PRIMARY KEY,
    account_id bigint NOT NULL,
    title varchar(255) NOT NULL DEFAULT '',
    context varchar(255)[] NOT NULL DEFAULT '{}',
    action integer NOT NULL DEFAULT 0,
    expires_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    
    CONSTRAINT fk_filters_account FOREIGN KEY (account_id) 
        REFERENCES accounts(id) ON DELETE CASCADE
);

CREATE INDEX index_filters_on_account_id ON filters (account_id);

-- Filter Keywords
CREATE TABLE filter_keywords (
    id bigserial PRIMARY KEY,
    filter_id bigint NOT NULL,
    keyword varchar(255) NOT NULL DEFAULT '',
    whole_word boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    
    CONSTRAINT fk_filter_keywords_filter FOREIGN KEY (filter_id) 
        REFERENCES filters(id) ON DELETE CASCADE
);

CREATE INDEX index_filter_keywords_on_filter_id ON filter_keywords (filter_id);

-- Filter Statuses
CREATE TABLE filter_statuses (
    id bigserial PRIMARY KEY,
    filter_id bigint NOT NULL,
    status_id bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    
    CONSTRAINT fk_filter_statuses_filter FOREIGN KEY (filter_id) 
        REFERENCES filters(id) ON DELETE CASCADE,
    CONSTRAINT fk_filter_statuses_status FOREIGN KEY (status_id)
        REFERENCES statuses(id) ON DELETE CASCADE
);

CREATE INDEX index_filter_statuses_on_filter_id ON filter_statuses (filter_id);
CREATE UNIQUE INDEX index_filter_statuses_on_filter_status ON filter_statuses (filter_id, status_id);

-- ============================================================================
-- Notifications
-- ============================================================================

-- Notifications
CREATE TABLE notifications (
    id bigserial PRIMARY KEY,
    activity_id bigint NOT NULL,
    activity_type varchar(255) NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    account_id bigint NOT NULL,
    from_account_id bigint NOT NULL,
    type varchar(255),
    filtered boolean NOT NULL DEFAULT false,
    group_key varchar(255),
    
    CONSTRAINT fk_notifications_account FOREIGN KEY (account_id) 
        REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT fk_notifications_from_account FOREIGN KEY (from_account_id) 
        REFERENCES accounts(id) ON DELETE CASCADE
);

CREATE INDEX index_notifications_on_account_id_and_id_and_type 
    ON notifications (account_id, id DESC, type);
CREATE INDEX index_notifications_on_activity_id_and_activity_type 
    ON notifications (activity_id, activity_type);
CREATE INDEX index_notifications_on_from_account_id ON notifications (from_account_id);

-- ============================================================================
-- Announcements
-- ============================================================================

-- Announcements
CREATE TABLE announcements (
    id bigserial PRIMARY KEY,
    text text NOT NULL DEFAULT '',
    published boolean NOT NULL DEFAULT false,
    all_day boolean NOT NULL DEFAULT false,
    scheduled_at timestamptz,
    starts_at timestamptz,
    ends_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz,
    status_ids bigint[]
);

-- Announcement Mutes
CREATE TABLE announcement_mutes (
    id bigserial PRIMARY KEY,
    account_id bigint NOT NULL,
    announcement_id bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    
    CONSTRAINT fk_announcement_mutes_account FOREIGN KEY (account_id) 
        REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT fk_announcement_mutes_announcement FOREIGN KEY (announcement_id) 
        REFERENCES announcements(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX index_announcement_mutes_on_account_announcement 
    ON announcement_mutes (account_id, announcement_id);

-- Announcement Reactions
CREATE TABLE announcement_reactions (
    id bigserial PRIMARY KEY,
    account_id bigint,
    announcement_id bigint NOT NULL,
    name varchar(255) NOT NULL DEFAULT '',
    custom_emoji_id bigint,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    
    CONSTRAINT fk_announcement_reactions_account FOREIGN KEY (account_id) 
        REFERENCES accounts(id) ON DELETE SET NULL,
    CONSTRAINT fk_announcement_reactions_announcement FOREIGN KEY (announcement_id) 
        REFERENCES announcements(id) ON DELETE CASCADE
);

CREATE INDEX index_announcement_reactions_on_announcement_id 
    ON announcement_reactions (announcement_id);
CREATE UNIQUE INDEX index_announcement_reactions_on_account_announcement_name 
    ON announcement_reactions (account_id, announcement_id, name) WHERE account_id IS NOT NULL;

-- ============================================================================
-- Custom Emojis
-- ============================================================================

-- Custom Emojis
CREATE TABLE custom_emojis (
    id bigserial PRIMARY KEY,
    shortcode varchar(255) NOT NULL DEFAULT '',
    domain varchar(255),
    image_file_name varchar(255),
    image_content_type varchar(255),
    image_file_size integer,
    image_updated_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    disabled boolean NOT NULL DEFAULT false,
    uri varchar(255),
    image_remote_url varchar(2048),
    visible_in_picker boolean NOT NULL DEFAULT true,
    category_id bigint,
    image_storage_schema_version integer
);

CREATE UNIQUE INDEX index_custom_emojis_on_shortcode_domain 
    ON custom_emojis (shortcode, COALESCE(domain, ''));
CREATE INDEX index_custom_emojis_on_domain_id ON custom_emojis (domain, id DESC);

-- ============================================================================
-- Push Notifications
-- ============================================================================

-- Web Push Subscriptions
CREATE TABLE web_push_subscriptions (
    id bigserial PRIMARY KEY,
    endpoint varchar(2048) NOT NULL,
    key_p256dh varchar(255),
    key_auth varchar(255),
    data jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    access_token_id bigint,
    user_id bigint,
    
    CONSTRAINT fk_web_push_subscriptions_access_token FOREIGN KEY (access_token_id) 
        REFERENCES oauth_access_tokens(id) ON DELETE CASCADE,
    CONSTRAINT fk_web_push_subscriptions_user FOREIGN KEY (user_id) 
        REFERENCES users(id) ON DELETE CASCADE
);

CREATE INDEX index_web_push_subscriptions_on_access_token_id ON web_push_subscriptions (access_token_id);
CREATE INDEX index_web_push_subscriptions_on_user_id ON web_push_subscriptions (user_id);

-- ============================================================================
-- Reports
-- ============================================================================

-- Reports
CREATE TABLE reports (
    id bigserial PRIMARY KEY,
    status_ids bigint[] NOT NULL DEFAULT '{}',
    comment text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    account_id bigint NOT NULL,
    action_taken_by_account_id bigint,
    target_account_id bigint NOT NULL,
    action_taken_at timestamptz,
    forwarded boolean,
    category integer NOT NULL DEFAULT 0,
    assigned_account_id bigint,
    rule_ids integer[] NOT NULL DEFAULT '{}',
    uri varchar(255),
    
    CONSTRAINT fk_reports_account FOREIGN KEY (account_id) 
        REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT fk_reports_action_taken_by FOREIGN KEY (action_taken_by_account_id) 
        REFERENCES accounts(id) ON DELETE SET NULL,
    CONSTRAINT fk_reports_target_account FOREIGN KEY (target_account_id) 
        REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT fk_reports_assigned FOREIGN KEY (assigned_account_id) 
        REFERENCES accounts(id) ON DELETE SET NULL
);

CREATE INDEX index_reports_on_account_id ON reports (account_id);
CREATE INDEX index_reports_on_target_account_id ON reports (target_account_id);
CREATE INDEX index_reports_on_assigned_account_id ON reports (assigned_account_id);

-- ============================================================================
-- Settings and Preferences
-- ============================================================================

-- Settings
CREATE TABLE settings (
    id bigserial PRIMARY KEY,
    var varchar(255) NOT NULL,
    value text,
    created_at timestamptz,
    updated_at timestamptz
);

CREATE UNIQUE INDEX index_settings_on_var ON settings (var);

-- Preferences
CREATE TABLE preferences (
    id bigserial PRIMARY KEY,
    account_id bigint NOT NULL UNIQUE,
    posting_default_visibility varchar(255) DEFAULT 'public',
    posting_default_sensitive boolean DEFAULT false,
    posting_default_language varchar(255),
    reading_expand_media varchar(255) DEFAULT 'default',
    reading_expand_spoilers boolean DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    
    CONSTRAINT fk_preferences_account FOREIGN KEY (account_id) 
        REFERENCES accounts(id) ON DELETE CASCADE
);

-- Featured Tags
CREATE TABLE featured_tags (
    id bigserial PRIMARY KEY,
    account_id bigint NOT NULL,
    tag_id bigint,
    name varchar(255) NOT NULL,
    statuses_count bigint DEFAULT 0,
    last_status_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    
    CONSTRAINT fk_featured_tags_account FOREIGN KEY (account_id) 
        REFERENCES accounts(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX index_featured_tags_on_account_id_and_name ON featured_tags (account_id, name);

-- Account Pins (Endorsements)
CREATE TABLE account_pins (
    id bigserial PRIMARY KEY,
    account_id bigint NOT NULL,
    target_account_id bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    
    CONSTRAINT fk_account_pins_account FOREIGN KEY (account_id) 
        REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT fk_account_pins_target_account FOREIGN KEY (target_account_id) 
        REFERENCES accounts(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX index_account_pins_on_account_and_target ON account_pins (account_id, target_account_id);

-- Markers (reading positions)
CREATE TABLE markers (
    id bigserial PRIMARY KEY,
    user_id bigint NOT NULL,
    timeline varchar(255) NOT NULL DEFAULT '',
    last_read_id bigint NOT NULL DEFAULT 0,
    lock_version integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    
    CONSTRAINT fk_markers_user FOREIGN KEY (user_id) 
        REFERENCES users(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX index_markers_on_user_timeline ON markers (user_id, timeline);

-- +goose Down
DROP TABLE IF EXISTS markers CASCADE;
DROP TABLE IF EXISTS preferences CASCADE;
DROP TABLE IF EXISTS settings CASCADE;
DROP TABLE IF EXISTS reports CASCADE;
DROP TABLE IF EXISTS web_push_subscriptions CASCADE;
DROP TABLE IF EXISTS custom_emojis CASCADE;
DROP TABLE IF EXISTS announcement_reactions CASCADE;
DROP TABLE IF EXISTS announcement_mutes CASCADE;
DROP TABLE IF EXISTS announcements CASCADE;
DROP TABLE IF EXISTS notifications CASCADE;
DROP TABLE IF EXISTS filter_statuses CASCADE;
DROP TABLE IF EXISTS filter_keywords CASCADE;
DROP TABLE IF EXISTS filters CASCADE;
DROP TABLE IF EXISTS poll_votes CASCADE;
DROP TABLE IF EXISTS polls CASCADE;
DROP TABLE IF EXISTS list_accounts CASCADE;
DROP TABLE IF EXISTS lists CASCADE;
DROP TABLE IF EXISTS statuses_tags CASCADE;
DROP TABLE IF EXISTS tags CASCADE;
DROP TABLE IF EXISTS mentions CASCADE;
DROP TABLE IF EXISTS conversation_mutes CASCADE;
DROP TABLE IF EXISTS domain_blocks CASCADE;
DROP TABLE IF EXISTS account_domain_blocks CASCADE;
DROP TABLE IF EXISTS mutes CASCADE;
DROP TABLE IF EXISTS blocks CASCADE;
DROP TABLE IF EXISTS bookmarks CASCADE;
DROP TABLE IF EXISTS favourites CASCADE;
DROP TABLE IF EXISTS follow_requests CASCADE;
DROP TABLE IF EXISTS follows CASCADE;
DROP TABLE IF EXISTS media_attachments CASCADE;
DROP TABLE IF EXISTS status_pins CASCADE;
DROP TABLE IF EXISTS scheduled_statuses CASCADE;
DROP TABLE IF EXISTS status_stats CASCADE;
DROP TABLE IF EXISTS statuses CASCADE;
DROP TABLE IF EXISTS conversations CASCADE;
DROP TABLE IF EXISTS oauth_access_tokens CASCADE;
DROP TABLE IF EXISTS oauth_applications CASCADE;
DROP TABLE IF EXISTS account_stats CASCADE;
DROP TABLE IF EXISTS users CASCADE;
DROP TABLE IF EXISTS accounts CASCADE;
DROP TABLE IF EXISTS user_roles CASCADE;
DROP FUNCTION IF EXISTS timestamp_id(text);
DROP SEQUENCE IF EXISTS scheduled_statuses_id_seq;
DROP SEQUENCE IF EXISTS media_attachments_id_seq;
DROP SEQUENCE IF EXISTS statuses_id_seq;
DROP SEQUENCE IF EXISTS accounts_id_seq;
