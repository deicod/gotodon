-- Migration 048: Unique Local Custom Emoji Shortcodes
-- index_custom_emojis_on_shortcode_and_domain is unique on (shortcode, domain),
-- as in Mastodon's schema. PostgreSQL treats NULLs as distinct, so it never
-- stops two local emojis (domain NULL) from sharing a shortcode. Mastodon
-- relies on the model's uniqueness validation for local emojis, which two
-- concurrent requests can both pass. This partial index makes the database
-- enforce it, as 001_initial_schema.sql's (shortcode, COALESCE(domain, ''))
-- index did until 036 replaced it with Mastodon's.
--
-- Matching is case-sensitive, as Mastodon's validation is on PostgreSQL.
--
-- Creating the index fails if local emojis already share a shortcode. Rename
-- or delete the duplicates, then run the migration again.

-- +goose Up
CREATE UNIQUE INDEX index_custom_emojis_on_shortcode_local
    ON custom_emojis (shortcode)
    WHERE domain IS NULL;

-- +goose Down
DROP INDEX IF EXISTS index_custom_emojis_on_shortcode_local;
