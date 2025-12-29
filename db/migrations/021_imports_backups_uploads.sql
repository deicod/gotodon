-- Migration 021: Imports, Backups, and Site Uploads
-- Adds backups, bulk_imports, bulk_import_rows, and site_uploads tables.

-- +goose Up

CREATE TABLE backups (
    id bigserial PRIMARY KEY,
    user_id bigint,
    dump_file_name varchar(255),
    dump_content_type varchar(255),
    dump_updated_at timestamptz,
    processed boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    dump_file_size bigint,

    CONSTRAINT fk_backups_user FOREIGN KEY (user_id)
        REFERENCES users(id) ON DELETE SET NULL
);

CREATE INDEX index_backups_on_user_id
    ON backups (user_id);

CREATE TABLE bulk_imports (
    id bigserial PRIMARY KEY,
    type integer NOT NULL,
    state integer NOT NULL,
    total_items integer NOT NULL DEFAULT 0,
    imported_items integer NOT NULL DEFAULT 0,
    processed_items integer NOT NULL DEFAULT 0,
    finished_at timestamptz,
    overwrite boolean NOT NULL DEFAULT false,
    likely_mismatched boolean NOT NULL DEFAULT false,
    original_filename varchar(255) NOT NULL DEFAULT '',
    account_id bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT fk_bulk_imports_account FOREIGN KEY (account_id)
        REFERENCES accounts(id) ON DELETE CASCADE
);

CREATE INDEX index_bulk_imports_on_account_id
    ON bulk_imports (account_id);

CREATE INDEX index_bulk_imports_unconfirmed
    ON bulk_imports (id)
    WHERE state = 0;

CREATE TABLE bulk_import_rows (
    id bigserial PRIMARY KEY,
    bulk_import_id bigint NOT NULL,
    data jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT fk_bulk_import_rows_import FOREIGN KEY (bulk_import_id)
        REFERENCES bulk_imports(id) ON DELETE CASCADE
);

CREATE INDEX index_bulk_import_rows_on_bulk_import_id
    ON bulk_import_rows (bulk_import_id);

CREATE TABLE site_uploads (
    id bigserial PRIMARY KEY,
    var varchar(255) NOT NULL DEFAULT '',
    file_file_name varchar(255),
    file_content_type varchar(255),
    file_file_size integer,
    file_updated_at timestamptz,
    meta json,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    blurhash varchar(255)
);

CREATE UNIQUE INDEX index_site_uploads_on_var
    ON site_uploads (var);

-- +goose Down

DROP TABLE IF EXISTS site_uploads CASCADE;
DROP TABLE IF EXISTS bulk_import_rows CASCADE;
DROP TABLE IF EXISTS bulk_imports CASCADE;
DROP TABLE IF EXISTS backups CASCADE;
