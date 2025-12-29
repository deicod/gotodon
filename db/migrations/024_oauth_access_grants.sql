-- Migration 024: OAuth Access Grants
-- Adds oauth_access_grants table for authorization code flow.

-- +goose Up

CREATE TABLE oauth_access_grants (
    id bigserial PRIMARY KEY,
    token varchar(255) NOT NULL,
    expires_in integer NOT NULL,
    redirect_uri text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz,
    scopes varchar(255),
    application_id bigint NOT NULL,
    resource_owner_id bigint NOT NULL,
    code_challenge varchar(255),
    code_challenge_method varchar(255),

    CONSTRAINT fk_oauth_access_grants_application FOREIGN KEY (application_id)
        REFERENCES oauth_applications(id) ON DELETE CASCADE,
    CONSTRAINT fk_oauth_access_grants_owner FOREIGN KEY (resource_owner_id)
        REFERENCES users(id) ON DELETE CASCADE
);

CREATE INDEX index_oauth_access_grants_on_resource_owner_id
    ON oauth_access_grants (resource_owner_id);

CREATE UNIQUE INDEX index_oauth_access_grants_on_token
    ON oauth_access_grants (token);

-- +goose Down

DROP TABLE IF EXISTS oauth_access_grants CASCADE;
