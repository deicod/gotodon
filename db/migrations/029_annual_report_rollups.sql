-- Migration 029: Annual Report Rollups
-- Adds annual_report_statuses_per_account_counts table.

-- +goose Up

CREATE TABLE annual_report_statuses_per_account_counts (
    id bigserial PRIMARY KEY,
    year integer NOT NULL,
    account_id bigint NOT NULL,
    statuses_count bigint NOT NULL
);

CREATE UNIQUE INDEX idx_on_year_account_id_ff3e167cef
    ON annual_report_statuses_per_account_counts (year, account_id);

-- +goose Down

DROP TABLE IF EXISTS annual_report_statuses_per_account_counts CASCADE;
