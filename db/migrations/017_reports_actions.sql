-- Migration 017: Reports and Admin Actions
-- Adds report_notes, appeals, and admin_action_logs tables.

-- +goose Up

CREATE TABLE admin_action_logs (
    id bigserial PRIMARY KEY,
    account_id bigint NOT NULL,
    action varchar(255) NOT NULL DEFAULT '',
    target_type varchar(255),
    target_id bigint,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    human_identifier varchar(255),
    route_param varchar(255),
    permalink varchar(255),

    CONSTRAINT fk_admin_action_logs_account FOREIGN KEY (account_id)
        REFERENCES accounts(id) ON DELETE CASCADE
);

CREATE INDEX index_admin_action_logs_on_account_id
    ON admin_action_logs (account_id);

CREATE INDEX index_admin_action_logs_on_target_type_and_target_id
    ON admin_action_logs (target_type, target_id);

CREATE TABLE appeals (
    id bigserial PRIMARY KEY,
    account_id bigint NOT NULL,
    account_warning_id bigint NOT NULL,
    text text NOT NULL DEFAULT '',
    approved_at timestamptz,
    approved_by_account_id bigint,
    rejected_at timestamptz,
    rejected_by_account_id bigint,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT fk_appeals_account FOREIGN KEY (account_id)
        REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT fk_appeals_warning FOREIGN KEY (account_warning_id)
        REFERENCES account_warnings(id) ON DELETE CASCADE,
    CONSTRAINT fk_appeals_approved_by FOREIGN KEY (approved_by_account_id)
        REFERENCES accounts(id) ON DELETE SET NULL,
    CONSTRAINT fk_appeals_rejected_by FOREIGN KEY (rejected_by_account_id)
        REFERENCES accounts(id) ON DELETE SET NULL
);

CREATE INDEX index_appeals_on_account_id
    ON appeals (account_id);

CREATE UNIQUE INDEX index_appeals_on_account_warning_id
    ON appeals (account_warning_id);

CREATE INDEX index_appeals_on_approved_by_account_id
    ON appeals (approved_by_account_id)
    WHERE approved_by_account_id IS NOT NULL;

CREATE INDEX index_appeals_on_rejected_by_account_id
    ON appeals (rejected_by_account_id)
    WHERE rejected_by_account_id IS NOT NULL;

CREATE TABLE report_notes (
    id bigserial PRIMARY KEY,
    content text NOT NULL,
    report_id bigint NOT NULL,
    account_id bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT fk_report_notes_report FOREIGN KEY (report_id)
        REFERENCES reports(id) ON DELETE CASCADE,
    CONSTRAINT fk_report_notes_account FOREIGN KEY (account_id)
        REFERENCES accounts(id) ON DELETE CASCADE
);

CREATE INDEX index_report_notes_on_account_id
    ON report_notes (account_id);

CREATE INDEX index_report_notes_on_report_id
    ON report_notes (report_id);

-- +goose Down

DROP TABLE IF EXISTS report_notes CASCADE;
DROP TABLE IF EXISTS appeals CASCADE;
DROP TABLE IF EXISTS admin_action_logs CASCADE;
