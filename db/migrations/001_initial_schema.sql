-- Gotodon Initial Schema
-- Based on Mastodon v4.5.2 database schema
-- Migration: 001_initial_schema

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

-- Insert default roles
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

-- Settings
CREATE TABLE settings (
    id bigserial PRIMARY KEY,
    var varchar(255) NOT NULL,
    value text,
    created_at timestamptz,
    updated_at timestamptz
);

CREATE UNIQUE INDEX index_settings_on_var ON settings (var);


-- ============================================================================
-- Down Migration (for rollback)
-- ============================================================================
-- DROP TABLE IF EXISTS settings CASCADE;
-- DROP TABLE IF EXISTS notifications CASCADE;
-- DROP TABLE IF EXISTS statuses_tags CASCADE;
-- DROP TABLE IF EXISTS tags CASCADE;
-- DROP TABLE IF EXISTS mentions CASCADE;
-- DROP TABLE IF EXISTS mutes CASCADE;
-- DROP TABLE IF EXISTS blocks CASCADE;
-- DROP TABLE IF EXISTS favourites CASCADE;
-- DROP TABLE IF EXISTS follow_requests CASCADE;
-- DROP TABLE IF EXISTS follows CASCADE;
-- DROP TABLE IF EXISTS media_attachments CASCADE;
-- DROP TABLE IF EXISTS status_stats CASCADE;
-- DROP TABLE IF EXISTS statuses CASCADE;
-- DROP TABLE IF EXISTS conversations CASCADE;
-- DROP TABLE IF EXISTS oauth_access_tokens CASCADE;
-- DROP TABLE IF EXISTS oauth_applications CASCADE;
-- DROP TABLE IF EXISTS account_stats CASCADE;
-- DROP TABLE IF EXISTS users CASCADE;
-- DROP TABLE IF EXISTS accounts CASCADE;
-- DROP TABLE IF EXISTS user_roles CASCADE;
-- DROP FUNCTION IF EXISTS timestamp_id(text);
-- DROP SEQUENCE IF EXISTS accounts_id_seq;
-- DROP SEQUENCE IF EXISTS statuses_id_seq;
-- DROP SEQUENCE IF EXISTS media_attachments_id_seq;
