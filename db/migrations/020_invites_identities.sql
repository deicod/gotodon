-- Migration 020: Invites and Identities
-- Adds invites, user_invite_requests, and identities tables.

-- +goose Up

CREATE TABLE identities (
    id bigserial PRIMARY KEY,
    provider varchar(255) NOT NULL DEFAULT '',
    uid varchar(255) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    user_id bigint,

    CONSTRAINT fk_identities_user FOREIGN KEY (user_id)
        REFERENCES users(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX index_identities_on_uid_and_provider
    ON identities (uid, provider);

CREATE INDEX index_identities_on_user_id
    ON identities (user_id);

CREATE TABLE invites (
    id bigserial PRIMARY KEY,
    user_id bigint NOT NULL,
    code varchar(255) NOT NULL DEFAULT '',
    expires_at timestamptz,
    max_uses integer,
    uses integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    autofollow boolean NOT NULL DEFAULT false,
    comment text,

    CONSTRAINT fk_invites_user FOREIGN KEY (user_id)
        REFERENCES users(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX index_invites_on_code
    ON invites (code);

CREATE INDEX index_invites_on_user_id
    ON invites (user_id);

CREATE TABLE user_invite_requests (
    id bigserial PRIMARY KEY,
    user_id bigint NOT NULL,
    text text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT fk_user_invite_requests_user FOREIGN KEY (user_id)
        REFERENCES users(id) ON DELETE CASCADE
);

CREATE INDEX index_user_invite_requests_on_user_id
    ON user_invite_requests (user_id);

-- +goose Down

DROP TABLE IF EXISTS user_invite_requests CASCADE;
DROP TABLE IF EXISTS invites CASCADE;
DROP TABLE IF EXISTS identities CASCADE;
