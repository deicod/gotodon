-- Migration 010: IP Blocks
-- Adds ip_blocks table for admin IP moderation

-- +goose Up

CREATE TABLE ip_blocks (
    id bigserial PRIMARY KEY,
    ip inet NOT NULL DEFAULT '0.0.0.0',
    severity integer NOT NULL DEFAULT 0,
    expires_at timestamptz,
    comment text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX index_ip_blocks_on_ip ON ip_blocks (ip);

-- +goose Down

DROP TABLE IF EXISTS ip_blocks CASCADE;
