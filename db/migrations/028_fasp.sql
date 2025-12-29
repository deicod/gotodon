-- Migration 028: FASP
-- Adds fasp_providers, fasp_backfill_requests, fasp_debug_callbacks,
-- fasp_follow_recommendations, and fasp_subscriptions tables.

-- +goose Up

CREATE TABLE fasp_providers (
    id bigserial PRIMARY KEY,
    confirmed boolean NOT NULL DEFAULT false,
    name varchar(255) NOT NULL,
    base_url varchar(255) NOT NULL,
    sign_in_url varchar(255),
    remote_identifier varchar(255) NOT NULL,
    provider_public_key_pem varchar(255) NOT NULL,
    server_private_key_pem varchar(255) NOT NULL,
    capabilities jsonb NOT NULL DEFAULT '[]'::jsonb,
    privacy_policy jsonb,
    contact_email varchar(255),
    fediverse_account varchar(255),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    delivery_last_failed_at timestamptz
);

CREATE UNIQUE INDEX index_fasp_providers_on_base_url
    ON fasp_providers (base_url);

CREATE TABLE fasp_backfill_requests (
    id bigserial PRIMARY KEY,
    category varchar(255) NOT NULL,
    max_count integer NOT NULL DEFAULT 100,
    cursor varchar(255),
    fulfilled boolean NOT NULL DEFAULT false,
    fasp_provider_id bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT fk_fasp_backfill_requests_provider FOREIGN KEY (fasp_provider_id)
        REFERENCES fasp_providers(id)
);

CREATE INDEX index_fasp_backfill_requests_on_fasp_provider_id
    ON fasp_backfill_requests (fasp_provider_id);

CREATE TABLE fasp_debug_callbacks (
    id bigserial PRIMARY KEY,
    fasp_provider_id bigint NOT NULL,
    ip varchar(255) NOT NULL,
    request_body text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT fk_fasp_debug_callbacks_provider FOREIGN KEY (fasp_provider_id)
        REFERENCES fasp_providers(id)
);

CREATE INDEX index_fasp_debug_callbacks_on_fasp_provider_id
    ON fasp_debug_callbacks (fasp_provider_id);

CREATE TABLE fasp_follow_recommendations (
    id bigserial PRIMARY KEY,
    requesting_account_id bigint NOT NULL,
    recommended_account_id bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT fk_fasp_follow_recommendations_requesting FOREIGN KEY (requesting_account_id)
        REFERENCES accounts(id),
    CONSTRAINT fk_fasp_follow_recommendations_recommended FOREIGN KEY (recommended_account_id)
        REFERENCES accounts(id)
);

CREATE INDEX index_fasp_follow_recommendations_on_recommended_account_id
    ON fasp_follow_recommendations (recommended_account_id);

CREATE INDEX index_fasp_follow_recommendations_on_requesting_account_id
    ON fasp_follow_recommendations (requesting_account_id);

CREATE TABLE fasp_subscriptions (
    id bigserial PRIMARY KEY,
    category varchar(255) NOT NULL,
    subscription_type varchar(255) NOT NULL,
    max_batch_size integer NOT NULL,
    threshold_timeframe integer,
    threshold_shares integer,
    threshold_likes integer,
    threshold_replies integer,
    fasp_provider_id bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT fk_fasp_subscriptions_provider FOREIGN KEY (fasp_provider_id)
        REFERENCES fasp_providers(id)
);

CREATE INDEX index_fasp_subscriptions_on_fasp_provider_id
    ON fasp_subscriptions (fasp_provider_id);

-- +goose Down

DROP TABLE IF EXISTS fasp_subscriptions CASCADE;
DROP TABLE IF EXISTS fasp_follow_recommendations CASCADE;
DROP TABLE IF EXISTS fasp_debug_callbacks CASCADE;
DROP TABLE IF EXISTS fasp_backfill_requests CASCADE;
DROP TABLE IF EXISTS fasp_providers CASCADE;
