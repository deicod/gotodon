-- Gotodon Schema Addition: Lists and Polls
-- Migration: 003_add_lists_polls

-- Lists
CREATE TABLE lists (
    id bigserial PRIMARY KEY,
    account_id bigint NOT NULL,
    title varchar(255) NOT NULL DEFAULT '',
    replies_policy integer NOT NULL DEFAULT 0, -- 0: followed, 1: list, 2: none
    exclusive boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    
    CONSTRAINT fk_lists_account FOREIGN KEY (account_id) 
        REFERENCES accounts(id) ON DELETE CASCADE
);

CREATE INDEX index_lists_on_account_id ON lists (account_id);

-- List Accounts (members of a list)
CREATE TABLE list_accounts (
    id bigserial PRIMARY KEY,
    list_id bigint NOT NULL,
    account_id bigint NOT NULL,
    follow_id bigint,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    
    CONSTRAINT fk_list_accounts_list FOREIGN KEY (list_id) 
        REFERENCES lists(id) ON DELETE CASCADE,
    CONSTRAINT fk_list_accounts_account FOREIGN KEY (account_id) 
        REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT fk_list_accounts_follow FOREIGN KEY (follow_id) 
        REFERENCES follows(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX index_list_accounts_on_list_and_account 
    ON list_accounts (list_id, account_id);
CREATE INDEX index_list_accounts_on_account_id ON list_accounts (account_id);
CREATE INDEX index_list_accounts_on_follow_id ON list_accounts (follow_id);

-- Polls
CREATE TABLE polls (
    id bigserial PRIMARY KEY,
    account_id bigint,
    status_id bigint,
    expires_at timestamptz NOT NULL,
    options varchar(255)[] NOT NULL DEFAULT '{}',
    cached_tallies jsonb NOT NULL DEFAULT '{}',
    multiple boolean NOT NULL DEFAULT false,
    hide_totals boolean NOT NULL DEFAULT false,
    votes_count bigint NOT NULL DEFAULT 0,
    voters_count bigint,
    last_fetched_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    lock_version integer NOT NULL DEFAULT 0,
    
    CONSTRAINT fk_polls_account FOREIGN KEY (account_id) 
        REFERENCES accounts(id) ON DELETE SET NULL,
    CONSTRAINT fk_polls_status FOREIGN KEY (status_id)
        REFERENCES statuses(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX index_polls_on_status_id ON polls (status_id);

-- Poll Votes
CREATE TABLE poll_votes (
    id bigserial PRIMARY KEY,
    account_id bigint,
    poll_id bigint NOT NULL,
    choice integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    uri varchar(255),
    
    CONSTRAINT fk_poll_votes_account FOREIGN KEY (account_id) 
        REFERENCES accounts(id) ON DELETE SET NULL,
    CONSTRAINT fk_poll_votes_poll FOREIGN KEY (poll_id)
        REFERENCES polls(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX index_poll_votes_on_poll_account_choice 
    ON poll_votes (poll_id, account_id, choice);
CREATE INDEX index_poll_votes_on_poll_id ON poll_votes (poll_id);

-- Add poll_id FK to statuses
ALTER TABLE statuses ADD CONSTRAINT fk_statuses_poll 
    FOREIGN KEY (poll_id) REFERENCES polls(id) ON DELETE SET NULL;
