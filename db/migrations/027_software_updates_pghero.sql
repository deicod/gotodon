-- Migration 027: Software Updates and PgHero
-- Adds software_updates and pghero_space_stats tables.

-- +goose Up

CREATE TABLE pghero_space_stats (
    id bigserial PRIMARY KEY,
    database text,
    schema text,
    relation text,
    size bigint,
    captured_at timestamptz
);

CREATE INDEX index_pghero_space_stats_on_database_and_captured_at
    ON pghero_space_stats (database, captured_at);

CREATE TABLE software_updates (
    id bigserial PRIMARY KEY,
    version varchar(255) NOT NULL,
    urgent boolean NOT NULL DEFAULT false,
    type integer NOT NULL DEFAULT 0,
    release_notes varchar(255) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX index_software_updates_on_version
    ON software_updates (version);

-- +goose Down

DROP TABLE IF EXISTS software_updates CASCADE;
DROP TABLE IF EXISTS pghero_space_stats CASCADE;
