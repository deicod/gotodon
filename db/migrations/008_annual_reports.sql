-- Migration 008: Annual Reports Table
-- Adds generated_annual_reports table for yearly wrap-up feature
-- Based on Mastodon v4.5.3 schema

-- +goose Up

-- ============================================================================
-- Generated Annual Reports (yearly wrap-up / Mastodon Wrapped)
-- ============================================================================

CREATE TABLE generated_annual_reports (
    id bigserial PRIMARY KEY,
    account_id bigint NOT NULL,
    year integer NOT NULL,
    data jsonb NOT NULL,
    schema_version integer NOT NULL,
    viewed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    
    CONSTRAINT fk_generated_annual_reports_account FOREIGN KEY (account_id) 
        REFERENCES accounts(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX index_generated_annual_reports_on_account_id_and_year 
    ON generated_annual_reports (account_id, year);

-- +goose Down

DROP TABLE IF EXISTS generated_annual_reports CASCADE;
