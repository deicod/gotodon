-- +goose Up
-- Add search_index to statuses
ALTER TABLE statuses ADD COLUMN search_index tsvector;
UPDATE statuses SET search_index = to_tsvector('english', text || ' ' || spoiler_text);
CREATE INDEX index_statuses_search ON statuses USING GIN (search_index);

CREATE OR REPLACE FUNCTION statuses_search_update() RETURNS trigger AS $$
BEGIN
  NEW.search_index := to_tsvector('english', NEW.text || ' ' || NEW.spoiler_text);
  RETURN NEW;
END
$$ LANGUAGE plpgsql;

CREATE TRIGGER statuses_search_update BEFORE INSERT OR UPDATE ON statuses
FOR EACH ROW EXECUTE PROCEDURE statuses_search_update();

-- Add search_index to accounts
ALTER TABLE accounts ADD COLUMN search_index tsvector;
UPDATE accounts SET search_index = to_tsvector('english', username || ' ' || display_name || ' ' || note);
CREATE INDEX index_accounts_search ON accounts USING GIN (search_index);

CREATE OR REPLACE FUNCTION accounts_search_update() RETURNS trigger AS $$
BEGIN
  NEW.search_index := to_tsvector('english', NEW.username || ' ' || NEW.display_name || ' ' || NEW.note);
  RETURN NEW;
END
$$ LANGUAGE plpgsql;

CREATE TRIGGER accounts_search_update BEFORE INSERT OR UPDATE ON accounts
FOR EACH ROW EXECUTE PROCEDURE accounts_search_update();

-- +goose Down
DROP TRIGGER IF EXISTS accounts_search_update ON accounts;
DROP FUNCTION IF EXISTS accounts_search_update;
DROP INDEX IF EXISTS index_accounts_search;
ALTER TABLE accounts DROP COLUMN search_index;

DROP TRIGGER IF EXISTS statuses_search_update ON statuses;
DROP FUNCTION IF EXISTS statuses_search_update;
DROP INDEX IF EXISTS index_statuses_search;
ALTER TABLE statuses DROP COLUMN search_index;
