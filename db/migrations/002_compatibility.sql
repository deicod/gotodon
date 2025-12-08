-- +goose Up

-- Account Pins (Endorsements)
-- Mastodon uses 'account_pins' table for endorsements
CREATE TABLE account_pins (
    id bigserial PRIMARY KEY,
    account_id bigint NOT NULL,
    target_account_id bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    
    CONSTRAINT fk_account_pins_account FOREIGN KEY (account_id) 
        REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT fk_account_pins_target_account FOREIGN KEY (target_account_id) 
        REFERENCES accounts(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX index_account_pins_on_account_and_target 
    ON account_pins (account_id, target_account_id);

-- Featured Tags
CREATE TABLE featured_tags (
    id bigserial PRIMARY KEY,
    account_id bigint NOT NULL,
    tag_id bigint NOT NULL,
    statuses_count bigint NOT NULL DEFAULT 0,
    last_status_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    name varchar(255), -- Denormalized name for display 

    CONSTRAINT fk_featured_tags_account FOREIGN KEY (account_id) 
        REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT fk_featured_tags_tag FOREIGN KEY (tag_id) 
        REFERENCES tags(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX index_featured_tags_on_account_and_tag 
    ON featured_tags (account_id, tag_id);

-- +goose Down
DROP TABLE IF EXISTS featured_tags;
DROP TABLE IF EXISTS account_pins;
