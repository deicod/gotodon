-- Gotodon Schema Addition: Filters
-- Migration: 002_add_filters

-- Filters (v2 API)
CREATE TABLE filters (
    id bigserial PRIMARY KEY,
    account_id bigint NOT NULL,
    title varchar(255) NOT NULL DEFAULT '',
    context varchar(255)[] NOT NULL DEFAULT '{}',
    action integer NOT NULL DEFAULT 0, -- 0: warn, 1: hide
    expires_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    
    CONSTRAINT fk_filters_account FOREIGN KEY (account_id) 
        REFERENCES accounts(id) ON DELETE CASCADE
);

CREATE INDEX index_filters_on_account_id ON filters (account_id);

-- Filter Keywords (v2 API)
CREATE TABLE filter_keywords (
    id bigserial PRIMARY KEY,
    filter_id bigint NOT NULL,
    keyword varchar(255) NOT NULL DEFAULT '',
    whole_word boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    
    CONSTRAINT fk_filter_keywords_filter FOREIGN KEY (filter_id) 
        REFERENCES filters(id) ON DELETE CASCADE
);

CREATE INDEX index_filter_keywords_on_filter_id ON filter_keywords (filter_id);

-- Filter Statuses (v2 API - filter specific statuses)
CREATE TABLE filter_statuses (
    id bigserial PRIMARY KEY,
    filter_id bigint NOT NULL,
    status_id bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    
    CONSTRAINT fk_filter_statuses_filter FOREIGN KEY (filter_id) 
        REFERENCES filters(id) ON DELETE CASCADE,
    CONSTRAINT fk_filter_statuses_status FOREIGN KEY (status_id)
        REFERENCES statuses(id) ON DELETE CASCADE
);

CREATE INDEX index_filter_statuses_on_filter_id ON filter_statuses (filter_id);
CREATE UNIQUE INDEX index_filter_statuses_on_filter_status ON filter_statuses (filter_id, status_id);

-- Bookmarks
CREATE TABLE bookmarks (
    id bigserial PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    account_id bigint NOT NULL,
    status_id bigint NOT NULL,
    
    CONSTRAINT fk_bookmarks_account FOREIGN KEY (account_id) 
        REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT fk_bookmarks_status FOREIGN KEY (status_id) 
        REFERENCES statuses(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX index_bookmarks_on_account_id_and_status_id 
    ON bookmarks (account_id, status_id);
CREATE INDEX index_bookmarks_on_status_id ON bookmarks (status_id);

-- Preferences
CREATE TABLE preferences (
    id bigserial PRIMARY KEY,
    account_id bigint NOT NULL UNIQUE,
    posting_default_visibility varchar(255) DEFAULT 'public',
    posting_default_sensitive boolean DEFAULT false,
    posting_default_language varchar(255),
    reading_expand_media varchar(255) DEFAULT 'default',
    reading_expand_spoilers boolean DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    
    CONSTRAINT fk_preferences_account FOREIGN KEY (account_id) 
        REFERENCES accounts(id) ON DELETE CASCADE
);
