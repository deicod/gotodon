-- Migration 012: Account Statuses Cleanup Policies
-- Adds per-account cleanup policy settings for automatic status deletion.

-- +goose Up

CREATE TABLE account_statuses_cleanup_policies (
    id bigserial PRIMARY KEY,
    account_id bigint NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    min_status_age integer NOT NULL DEFAULT 1209600,
    keep_direct boolean NOT NULL DEFAULT true,
    keep_pinned boolean NOT NULL DEFAULT true,
    keep_polls boolean NOT NULL DEFAULT false,
    keep_media boolean NOT NULL DEFAULT false,
    keep_self_fav boolean NOT NULL DEFAULT true,
    keep_self_bookmark boolean NOT NULL DEFAULT true,
    min_favs integer,
    min_reblogs integer,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT fk_account_statuses_cleanup_policies_account FOREIGN KEY (account_id)
        REFERENCES accounts(id) ON DELETE CASCADE
);

CREATE INDEX index_account_statuses_cleanup_policies_on_account_id
    ON account_statuses_cleanup_policies (account_id);

-- +goose Down

DROP TABLE IF EXISTS account_statuses_cleanup_policies CASCADE;
