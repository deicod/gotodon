-- Migration 036: Align Indexes (Missing Mastodon Indexes)
-- Adds missing indexes by Mastodon names and removes renamed extras.

-- +goose Up

DROP INDEX IF EXISTS index_account_domain_blocks_on_account_domain;
CREATE UNIQUE INDEX index_account_domain_blocks_on_account_id_and_domain
    ON account_domain_blocks (account_id, domain);

DROP INDEX IF EXISTS index_account_pins_on_account_and_target;
CREATE UNIQUE INDEX index_account_pins_on_account_id_and_target_account_id
    ON account_pins (account_id, target_account_id);
CREATE INDEX index_account_pins_on_target_account_id
    ON account_pins (target_account_id);

CREATE INDEX index_account_stats_on_last_status_at_and_account_id
    ON account_stats (last_status_at DESC NULLS LAST, account_id);

DROP INDEX IF EXISTS index_announcement_mutes_on_account_announcement;
CREATE UNIQUE INDEX index_announcement_mutes_on_account_id_and_announcement_id
    ON announcement_mutes (account_id, announcement_id);
CREATE INDEX index_announcement_mutes_on_announcement_id
    ON announcement_mutes (announcement_id);

DROP INDEX IF EXISTS index_announcement_reactions_on_account_announcement_name;
CREATE UNIQUE INDEX index_announcement_reactions_on_account_id_and_announcement_id
    ON announcement_reactions (account_id, announcement_id, name);
CREATE INDEX index_announcement_reactions_on_custom_emoji_id
    ON announcement_reactions (custom_emoji_id)
    WHERE custom_emoji_id IS NOT NULL;

CREATE UNIQUE INDEX index_conversations_on_parent_status_id
    ON conversations (parent_status_id)
    WHERE parent_status_id IS NOT NULL;

DROP INDEX IF EXISTS index_custom_emojis_on_shortcode_domain;
DROP INDEX IF EXISTS index_custom_emojis_on_domain_id;
CREATE UNIQUE INDEX index_custom_emojis_on_shortcode_and_domain
    ON custom_emojis (shortcode, domain);

CREATE INDEX index_favourites_on_account_id_and_id
    ON favourites (account_id, id);

DROP INDEX IF EXISTS index_featured_tags_on_account_id_and_name;
CREATE UNIQUE INDEX index_featured_tags_on_account_id_and_tag_id
    ON featured_tags (account_id, tag_id);
CREATE INDEX index_featured_tags_on_tag_id
    ON featured_tags (tag_id);

DROP INDEX IF EXISTS index_markers_on_user_timeline;
CREATE UNIQUE INDEX index_markers_on_user_id_and_timeline
    ON markers (user_id, timeline);

CREATE INDEX index_media_attachments_on_scheduled_status_id
    ON media_attachments (scheduled_status_id)
    WHERE scheduled_status_id IS NOT NULL;

CREATE INDEX index_notifications_on_filtered
    ON notifications (account_id, id DESC, type)
    WHERE filtered = false;

DROP INDEX IF EXISTS index_notification_permissions_unique;

CREATE INDEX index_oauth_applications_on_owner_id_and_owner_type
    ON oauth_applications (owner_id, owner_type);
CREATE INDEX index_oauth_applications_on_superapp
    ON oauth_applications (superapp)
    WHERE superapp = true;

CREATE INDEX index_poll_votes_on_account_id
    ON poll_votes (account_id);

DROP INDEX IF EXISTS index_poll_votes_on_poll_account_choice;

CREATE INDEX index_polls_on_account_id
    ON polls (account_id);

CREATE INDEX index_reports_on_action_taken_by_account_id
    ON reports (action_taken_by_account_id)
    WHERE action_taken_by_account_id IS NOT NULL;

DROP INDEX IF EXISTS index_status_pins_on_account_status;
CREATE UNIQUE INDEX index_status_pins_on_account_id_and_status_id
    ON status_pins (account_id, status_id);

CREATE INDEX index_statuses_20190820
    ON statuses (account_id, id DESC, visibility, updated_at)
    WHERE deleted_at IS NULL;
CREATE INDEX index_statuses_local_20190824
    ON statuses (id DESC, account_id)
    WHERE ((local OR (uri IS NULL)) AND (deleted_at IS NULL) AND (visibility = 0) AND (reblog_of_id IS NULL) AND ((NOT reply) OR (in_reply_to_account_id = account_id)));
CREATE INDEX index_statuses_public_20250129
    ON statuses (id DESC, language, account_id)
    WHERE ((deleted_at IS NULL) AND (visibility = 0) AND (reblog_of_id IS NULL) AND ((NOT reply) OR (in_reply_to_account_id = account_id)));
CREATE INDEX index_statuses_on_deleted_at
    ON statuses (deleted_at)
    WHERE deleted_at IS NOT NULL;
CREATE INDEX index_statuses_on_in_reply_to_account_id
    ON statuses (in_reply_to_account_id)
    WHERE in_reply_to_account_id IS NOT NULL;

CREATE INDEX index_users_on_created_by_application_id
    ON users (created_by_application_id)
    WHERE created_by_application_id IS NOT NULL;
CREATE INDEX index_users_on_role_id
    ON users (role_id)
    WHERE role_id IS NOT NULL;
CREATE INDEX index_users_on_unconfirmed_email
    ON users (unconfirmed_email)
    WHERE unconfirmed_email IS NOT NULL;

-- +goose Down

DROP INDEX IF EXISTS index_users_on_unconfirmed_email;
DROP INDEX IF EXISTS index_users_on_role_id;
DROP INDEX IF EXISTS index_users_on_created_by_application_id;

DROP INDEX IF EXISTS index_statuses_on_in_reply_to_account_id;
DROP INDEX IF EXISTS index_statuses_on_deleted_at;
DROP INDEX IF EXISTS index_statuses_public_20250129;
DROP INDEX IF EXISTS index_statuses_local_20190824;
DROP INDEX IF EXISTS index_statuses_20190820;

DROP INDEX IF EXISTS index_status_pins_on_account_id_and_status_id;
CREATE UNIQUE INDEX index_status_pins_on_account_status
    ON status_pins (account_id, status_id);

DROP INDEX IF EXISTS index_reports_on_action_taken_by_account_id;

DROP INDEX IF EXISTS index_polls_on_account_id;

DROP INDEX IF EXISTS index_poll_votes_on_account_id;

CREATE UNIQUE INDEX index_poll_votes_on_poll_account_choice
    ON poll_votes (poll_id, account_id, choice);

DROP INDEX IF EXISTS index_oauth_applications_on_superapp;
DROP INDEX IF EXISTS index_oauth_applications_on_owner_id_and_owner_type;

CREATE UNIQUE INDEX index_notification_permissions_unique
    ON notification_permissions (account_id, from_account_id);

DROP INDEX IF EXISTS index_notifications_on_filtered;

DROP INDEX IF EXISTS index_media_attachments_on_scheduled_status_id;

DROP INDEX IF EXISTS index_markers_on_user_id_and_timeline;
CREATE UNIQUE INDEX index_markers_on_user_timeline
    ON markers (user_id, timeline);

DROP INDEX IF EXISTS index_featured_tags_on_tag_id;
DROP INDEX IF EXISTS index_featured_tags_on_account_id_and_tag_id;
CREATE UNIQUE INDEX index_featured_tags_on_account_id_and_name
    ON featured_tags (account_id, name);

DROP INDEX IF EXISTS index_favourites_on_account_id_and_id;

DROP INDEX IF EXISTS index_custom_emojis_on_shortcode_and_domain;
CREATE UNIQUE INDEX index_custom_emojis_on_shortcode_domain
    ON custom_emojis (shortcode, COALESCE(domain, ''));
CREATE INDEX index_custom_emojis_on_domain_id
    ON custom_emojis (domain, id DESC);

DROP INDEX IF EXISTS index_conversations_on_parent_status_id;

DROP INDEX IF EXISTS index_announcement_reactions_on_custom_emoji_id;
DROP INDEX IF EXISTS index_announcement_reactions_on_account_id_and_announcement_id;
CREATE UNIQUE INDEX index_announcement_reactions_on_account_announcement_name
    ON announcement_reactions (account_id, announcement_id, name)
    WHERE account_id IS NOT NULL;

DROP INDEX IF EXISTS index_announcement_mutes_on_announcement_id;
DROP INDEX IF EXISTS index_announcement_mutes_on_account_id_and_announcement_id;
CREATE UNIQUE INDEX index_announcement_mutes_on_account_announcement
    ON announcement_mutes (account_id, announcement_id);

DROP INDEX IF EXISTS index_account_stats_on_last_status_at_and_account_id;

DROP INDEX IF EXISTS index_account_pins_on_target_account_id;
DROP INDEX IF EXISTS index_account_pins_on_account_id_and_target_account_id;
CREATE UNIQUE INDEX index_account_pins_on_account_and_target
    ON account_pins (account_id, target_account_id);

DROP INDEX IF EXISTS index_account_domain_blocks_on_account_id_and_domain;
CREATE UNIQUE INDEX index_account_domain_blocks_on_account_domain
    ON account_domain_blocks (account_id, domain);
