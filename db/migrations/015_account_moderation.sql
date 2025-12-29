-- Migration 015: Account Moderation
-- Adds moderation and safety tables tied to accounts.

-- +goose Up

-- Account moderation notes
CREATE TABLE account_moderation_notes (
    id bigserial PRIMARY KEY,
    content text NOT NULL,
    account_id bigint NOT NULL,
    target_account_id bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT fk_account_moderation_notes_account FOREIGN KEY (account_id)
        REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT fk_account_moderation_notes_target FOREIGN KEY (target_account_id)
        REFERENCES accounts(id) ON DELETE CASCADE
);

CREATE INDEX index_account_moderation_notes_on_account_id
    ON account_moderation_notes (account_id);

CREATE INDEX index_account_moderation_notes_on_target_account_id
    ON account_moderation_notes (target_account_id);

-- Account warning presets
CREATE TABLE account_warning_presets (
    id bigserial PRIMARY KEY,
    text text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    title varchar(255) NOT NULL DEFAULT ''
);

-- Account warnings
CREATE TABLE account_warnings (
    id bigserial PRIMARY KEY,
    account_id bigint,
    target_account_id bigint,
    action integer NOT NULL DEFAULT 0,
    text text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    report_id bigint,
    status_ids varchar(255)[],
    overruled_at timestamptz,

    CONSTRAINT fk_account_warnings_account FOREIGN KEY (account_id)
        REFERENCES accounts(id) ON DELETE SET NULL,
    CONSTRAINT fk_account_warnings_target FOREIGN KEY (target_account_id)
        REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT fk_account_warnings_report FOREIGN KEY (report_id)
        REFERENCES reports(id) ON DELETE CASCADE
);

CREATE INDEX index_account_warnings_on_account_id
    ON account_warnings (account_id);

CREATE INDEX index_account_warnings_on_target_account_id
    ON account_warnings (target_account_id);

-- Instance moderation notes
CREATE TABLE instance_moderation_notes (
    id bigserial PRIMARY KEY,
    domain varchar(255) NOT NULL,
    account_id bigint NOT NULL,
    content text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT fk_instance_moderation_notes_account FOREIGN KEY (account_id)
        REFERENCES accounts(id) ON DELETE CASCADE
);

CREATE INDEX index_instance_moderation_notes_on_domain
    ON instance_moderation_notes (domain);

-- Username blocks
CREATE TABLE username_blocks (
    id bigserial PRIMARY KEY,
    username varchar(255) NOT NULL,
    normalized_username varchar(255) NOT NULL,
    exact boolean NOT NULL DEFAULT false,
    allow_with_approval boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX index_username_blocks_on_username_lower_btree
    ON username_blocks (lower(username));

CREATE INDEX index_username_blocks_on_normalized_username
    ON username_blocks (normalized_username);

-- +goose Down

DROP TABLE IF EXISTS username_blocks CASCADE;
DROP TABLE IF EXISTS instance_moderation_notes CASCADE;
DROP TABLE IF EXISTS account_warnings CASCADE;
DROP TABLE IF EXISTS account_warning_presets CASCADE;
DROP TABLE IF EXISTS account_moderation_notes CASCADE;
