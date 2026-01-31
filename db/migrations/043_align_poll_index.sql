-- Align poll status index with Mastodon (non-unique).

-- +goose Up

DROP INDEX IF EXISTS index_polls_on_status_id;
CREATE INDEX index_polls_on_status_id ON polls (status_id);

-- +goose Down

DROP INDEX IF EXISTS index_polls_on_status_id;
CREATE UNIQUE INDEX index_polls_on_status_id ON polls (status_id);
