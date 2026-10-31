-- Migration 049: Announcement Reactions Custom Emoji Foreign Key
-- Adds Mastodon's foreign key from announcement_reactions.custom_emoji_id to
-- custom_emojis (add_foreign_key "announcement_reactions", "custom_emojis",
-- on_delete: :cascade), so deleting a custom emoji deletes the reactions
-- made with it instead of leaving them pointing at nothing.
--
-- Reactions whose custom emoji is already gone are deleted first, as the
-- cascade would have deleted them. The down migration drops the key and
-- can't bring those reactions back.

-- +goose Up
DELETE FROM announcement_reactions
WHERE custom_emoji_id IS NOT NULL
  AND NOT EXISTS (
    SELECT 1 FROM custom_emojis WHERE custom_emojis.id = announcement_reactions.custom_emoji_id
  );

ALTER TABLE announcement_reactions
    ADD CONSTRAINT fk_announcement_reactions_custom_emoji FOREIGN KEY (custom_emoji_id)
        REFERENCES custom_emojis(id) ON DELETE CASCADE;

-- +goose Down
ALTER TABLE announcement_reactions
    DROP CONSTRAINT IF EXISTS fk_announcement_reactions_custom_emoji;
