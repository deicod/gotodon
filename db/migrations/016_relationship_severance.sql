-- Migration 016: Relationship Severance
-- Adds relationship severance event tracking tables.

-- +goose Up

CREATE TABLE relationship_severance_events (
    id bigserial PRIMARY KEY,
    type integer NOT NULL,
    target_name varchar(255) NOT NULL,
    purged boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX index_relationship_severance_events_on_type_and_target_name
    ON relationship_severance_events (type, target_name);

CREATE TABLE account_relationship_severance_events (
    id bigserial PRIMARY KEY,
    account_id bigint NOT NULL,
    relationship_severance_event_id bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    followers_count integer NOT NULL DEFAULT 0,
    following_count integer NOT NULL DEFAULT 0,

    CONSTRAINT fk_account_relationship_severance_events_account FOREIGN KEY (account_id)
        REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT fk_account_relationship_severance_events_event FOREIGN KEY (relationship_severance_event_id)
        REFERENCES relationship_severance_events(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX idx_on_account_id_relationship_severance_event_id_7bd82bf20e
    ON account_relationship_severance_events (account_id, relationship_severance_event_id);

CREATE INDEX idx_on_relationship_severance_event_id_403f53e707
    ON account_relationship_severance_events (relationship_severance_event_id);

CREATE TABLE severed_relationships (
    id bigserial PRIMARY KEY,
    relationship_severance_event_id bigint NOT NULL,
    local_account_id bigint NOT NULL,
    remote_account_id bigint NOT NULL,
    direction integer NOT NULL,
    show_reblogs boolean,
    notify boolean,
    languages varchar(255)[],
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT fk_severed_relationships_event FOREIGN KEY (relationship_severance_event_id)
        REFERENCES relationship_severance_events(id) ON DELETE CASCADE,
    CONSTRAINT fk_severed_relationships_local_account FOREIGN KEY (local_account_id)
        REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT fk_severed_relationships_remote_account FOREIGN KEY (remote_account_id)
        REFERENCES accounts(id) ON DELETE CASCADE
);

CREATE INDEX index_severed_relationships_on_local_account_and_event
    ON severed_relationships (local_account_id, relationship_severance_event_id);

CREATE UNIQUE INDEX index_severed_relationships_on_unique_tuples
    ON severed_relationships (relationship_severance_event_id, local_account_id, direction, remote_account_id);

CREATE INDEX index_severed_relationships_on_remote_account_id
    ON severed_relationships (remote_account_id);

-- +goose Down

DROP TABLE IF EXISTS severed_relationships CASCADE;
DROP TABLE IF EXISTS account_relationship_severance_events CASCADE;
DROP TABLE IF EXISTS relationship_severance_events CASCADE;
