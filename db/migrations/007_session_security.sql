-- Migration 007: Session and Security Tables
-- Adds session_activations, login_activities, and webauthn_credentials tables
-- Based on Mastodon v4.5.3 schema

-- +goose Up

-- ============================================================================
-- Session Activations (active user sessions)
-- ============================================================================

CREATE TABLE session_activations (
    id bigserial PRIMARY KEY,
    session_id varchar(255) NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    user_agent varchar(1024) NOT NULL DEFAULT '',
    ip inet,
    access_token_id bigint,
    user_id bigint NOT NULL,
    web_push_subscription_id bigint,
    
    CONSTRAINT fk_session_activations_user FOREIGN KEY (user_id) 
        REFERENCES users(id) ON DELETE CASCADE,
    CONSTRAINT fk_session_activations_access_token FOREIGN KEY (access_token_id) 
        REFERENCES oauth_access_tokens(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX index_session_activations_on_session_id 
    ON session_activations (session_id);
CREATE INDEX index_session_activations_on_access_token_id 
    ON session_activations (access_token_id);
CREATE INDEX index_session_activations_on_user_id 
    ON session_activations (user_id);

-- ============================================================================
-- Login Activities (authentication audit log)
-- ============================================================================

CREATE TABLE login_activities (
    id bigserial PRIMARY KEY,
    user_id bigint NOT NULL,
    authentication_method varchar(255),
    provider varchar(255),
    success boolean,
    failure_reason varchar(255),
    ip inet,
    user_agent varchar(1024),
    created_at timestamptz DEFAULT now(),
    
    CONSTRAINT fk_login_activities_user FOREIGN KEY (user_id) 
        REFERENCES users(id) ON DELETE CASCADE
);

CREATE INDEX index_login_activities_on_user_id ON login_activities (user_id);

-- ============================================================================
-- WebAuthn Credentials (hardware security keys / passkeys)
-- ============================================================================

CREATE TABLE webauthn_credentials (
    id bigserial PRIMARY KEY,
    external_id varchar(255) NOT NULL,
    public_key text NOT NULL,
    nickname varchar(255) NOT NULL,
    sign_count bigint NOT NULL DEFAULT 0,
    user_id bigint,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    
    CONSTRAINT fk_webauthn_credentials_user FOREIGN KEY (user_id) 
        REFERENCES users(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX index_webauthn_credentials_on_external_id 
    ON webauthn_credentials (external_id);
CREATE UNIQUE INDEX index_webauthn_credentials_on_user_id_and_nickname 
    ON webauthn_credentials (user_id, nickname);

-- +goose Down

DROP TABLE IF EXISTS webauthn_credentials CASCADE;
DROP TABLE IF EXISTS login_activities CASCADE;
DROP TABLE IF EXISTS session_activations CASCADE;
