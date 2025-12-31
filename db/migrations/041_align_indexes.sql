-- Migration 041: Align Index Definitions with Mastodon
-- Adjusts index expressions, partial clauses, opclasses, and uniqueness to match Mastodon schema.

-- +goose Up

DROP INDEX IF EXISTS index_account_migrations_on_target_account_id;
CREATE INDEX index_account_migrations_on_target_account_id ON account_migrations (target_account_id) WHERE (target_account_id IS NOT NULL);

DROP INDEX IF EXISTS index_accounts_on_moved_to_account_id;
CREATE INDEX index_accounts_on_moved_to_account_id ON accounts (moved_to_account_id) WHERE (moved_to_account_id IS NOT NULL);

DROP INDEX IF EXISTS index_accounts_on_url;
CREATE INDEX index_accounts_on_url ON accounts (url text_pattern_ops) WHERE (url IS NOT NULL);

DROP INDEX IF EXISTS index_accounts_on_username_and_domain_lower;
CREATE UNIQUE INDEX index_accounts_on_username_and_domain_lower ON accounts (lower((username)::text), COALESCE(lower((domain)::text), ''::text));

DROP INDEX IF EXISTS index_announcement_reactions_on_custom_emoji_id;
CREATE INDEX index_announcement_reactions_on_custom_emoji_id ON announcement_reactions (custom_emoji_id) WHERE (custom_emoji_id IS NOT NULL);

DROP INDEX IF EXISTS index_appeals_on_approved_by_account_id;
CREATE INDEX index_appeals_on_approved_by_account_id ON appeals (approved_by_account_id) WHERE (approved_by_account_id IS NOT NULL);

DROP INDEX IF EXISTS index_appeals_on_rejected_by_account_id;
CREATE INDEX index_appeals_on_rejected_by_account_id ON appeals (rejected_by_account_id) WHERE (rejected_by_account_id IS NOT NULL);

DROP INDEX IF EXISTS index_bulk_imports_unconfirmed;
CREATE INDEX index_bulk_imports_unconfirmed ON bulk_imports (id) WHERE (state = 0);

DROP INDEX IF EXISTS index_conversations_on_parent_status_id;
CREATE UNIQUE INDEX index_conversations_on_parent_status_id ON conversations (parent_status_id) WHERE (parent_status_id IS NOT NULL);

DROP INDEX IF EXISTS index_conversations_on_uri;
CREATE UNIQUE INDEX index_conversations_on_uri ON conversations (uri text_pattern_ops) WHERE (uri IS NOT NULL);

DROP INDEX IF EXISTS index_list_accounts_on_follow_id;
CREATE INDEX index_list_accounts_on_follow_id ON list_accounts (follow_id) WHERE (follow_id IS NOT NULL);

DROP INDEX IF EXISTS index_list_accounts_on_follow_request_id;
CREATE INDEX index_list_accounts_on_follow_request_id ON list_accounts (follow_request_id) WHERE (follow_request_id IS NOT NULL);

DROP INDEX IF EXISTS index_media_attachments_on_scheduled_status_id;
CREATE INDEX index_media_attachments_on_scheduled_status_id ON media_attachments (scheduled_status_id) WHERE (scheduled_status_id IS NOT NULL);

DROP INDEX IF EXISTS index_media_attachments_on_shortcode;
CREATE UNIQUE INDEX index_media_attachments_on_shortcode ON media_attachments (shortcode text_pattern_ops) WHERE (shortcode IS NOT NULL);

DROP INDEX IF EXISTS index_notifications_on_account_id_and_group_key;
CREATE INDEX index_notifications_on_account_id_and_group_key ON notifications (account_id, group_key) WHERE (group_key IS NOT NULL);

DROP INDEX IF EXISTS index_notifications_on_filtered;
CREATE INDEX index_notifications_on_filtered ON notifications (account_id, id desc, type) WHERE (filtered = false);

DROP INDEX IF EXISTS index_oauth_access_tokens_on_refresh_token;
CREATE UNIQUE INDEX index_oauth_access_tokens_on_refresh_token ON oauth_access_tokens (refresh_token text_pattern_ops) WHERE (refresh_token IS NOT NULL);

DROP INDEX IF EXISTS index_oauth_access_tokens_on_resource_owner_id;
CREATE INDEX index_oauth_access_tokens_on_resource_owner_id ON oauth_access_tokens (resource_owner_id) WHERE (resource_owner_id IS NOT NULL);

DROP INDEX IF EXISTS index_oauth_applications_on_superapp;
CREATE INDEX index_oauth_applications_on_superapp ON oauth_applications (superapp) WHERE (superapp = true);

DROP INDEX IF EXISTS index_polls_on_status_id;
CREATE UNIQUE INDEX index_polls_on_status_id ON polls (status_id);

DROP INDEX IF EXISTS index_preview_cards_on_author_account_id;
CREATE INDEX index_preview_cards_on_author_account_id ON preview_cards (author_account_id) WHERE (author_account_id IS NOT NULL);

DROP INDEX IF EXISTS index_quotes_on_activity_uri;
CREATE UNIQUE INDEX index_quotes_on_activity_uri ON quotes (activity_uri) WHERE (activity_uri IS NOT NULL);

DROP INDEX IF EXISTS index_quotes_on_approval_uri;
CREATE INDEX index_quotes_on_approval_uri ON quotes (approval_uri) WHERE (approval_uri IS NOT NULL);

DROP INDEX IF EXISTS index_reports_on_action_taken_by_account_id;
CREATE INDEX index_reports_on_action_taken_by_account_id ON reports (action_taken_by_account_id) WHERE (action_taken_by_account_id IS NOT NULL);

DROP INDEX IF EXISTS index_reports_on_assigned_account_id;
CREATE INDEX index_reports_on_assigned_account_id ON reports (assigned_account_id) WHERE (assigned_account_id IS NOT NULL);

DROP INDEX IF EXISTS index_statuses_20190820;
CREATE INDEX index_statuses_20190820 ON statuses (account_id, id desc, visibility, updated_at) WHERE (deleted_at IS NULL);

DROP INDEX IF EXISTS index_statuses_on_deleted_at;
CREATE INDEX index_statuses_on_deleted_at ON statuses (deleted_at) WHERE (deleted_at IS NOT NULL);

DROP INDEX IF EXISTS index_statuses_on_in_reply_to_account_id;
CREATE INDEX index_statuses_on_in_reply_to_account_id ON statuses (in_reply_to_account_id) WHERE (in_reply_to_account_id IS NOT NULL);

DROP INDEX IF EXISTS index_statuses_on_in_reply_to_id;
CREATE INDEX index_statuses_on_in_reply_to_id ON statuses (in_reply_to_id) WHERE (in_reply_to_id IS NOT NULL);

DROP INDEX IF EXISTS index_statuses_on_uri;
CREATE UNIQUE INDEX index_statuses_on_uri ON statuses (uri text_pattern_ops) WHERE (uri IS NOT NULL);

DROP INDEX IF EXISTS index_tags_on_name_lower_btree;
CREATE UNIQUE INDEX index_tags_on_name_lower_btree ON tags (lower((name)::text) text_pattern_ops);

DROP INDEX IF EXISTS index_terms_of_services_on_effective_date;
CREATE UNIQUE INDEX index_terms_of_services_on_effective_date ON terms_of_services (effective_date) WHERE (effective_date IS NOT NULL);

DROP INDEX IF EXISTS index_username_blocks_on_username_lower_btree;
CREATE UNIQUE INDEX index_username_blocks_on_username_lower_btree ON username_blocks (lower((username)::text));

DROP INDEX IF EXISTS index_users_on_created_by_application_id;
CREATE INDEX index_users_on_created_by_application_id ON users (created_by_application_id) WHERE (created_by_application_id IS NOT NULL);

DROP INDEX IF EXISTS index_users_on_reset_password_token;
CREATE UNIQUE INDEX index_users_on_reset_password_token ON users (reset_password_token text_pattern_ops) WHERE (reset_password_token IS NOT NULL);

DROP INDEX IF EXISTS index_users_on_role_id;
CREATE INDEX index_users_on_role_id ON users (role_id) WHERE (role_id IS NOT NULL);

DROP INDEX IF EXISTS index_users_on_unconfirmed_email;
CREATE INDEX index_users_on_unconfirmed_email ON users (unconfirmed_email) WHERE (unconfirmed_email IS NOT NULL);

DROP INDEX IF EXISTS index_web_push_subscriptions_on_access_token_id;
CREATE INDEX index_web_push_subscriptions_on_access_token_id ON web_push_subscriptions (access_token_id) WHERE (access_token_id IS NOT NULL);

-- +goose Down

DROP INDEX IF EXISTS index_account_migrations_on_target_account_id;
CREATE INDEX index_account_migrations_on_target_account_id ON account_migrations (target_account_id) WHERE target_account_id IS NOT NULL;

DROP INDEX IF EXISTS index_accounts_on_moved_to_account_id;
CREATE INDEX index_accounts_on_moved_to_account_id ON accounts (moved_to_account_id) WHERE moved_to_account_id IS NOT NULL;

DROP INDEX IF EXISTS index_accounts_on_url;
CREATE INDEX index_accounts_on_url ON accounts (url text_pattern_ops) WHERE url IS NOT NULL;

DROP INDEX IF EXISTS index_accounts_on_username_and_domain_lower;
CREATE UNIQUE INDEX index_accounts_on_username_and_domain_lower ON accounts (lower(username), COALESCE(lower(domain), ''));

DROP INDEX IF EXISTS index_announcement_reactions_on_custom_emoji_id;
CREATE INDEX index_announcement_reactions_on_custom_emoji_id ON announcement_reactions (custom_emoji_id) WHERE custom_emoji_id IS NOT NULL;

DROP INDEX IF EXISTS index_appeals_on_approved_by_account_id;
CREATE INDEX index_appeals_on_approved_by_account_id ON appeals (approved_by_account_id) WHERE approved_by_account_id IS NOT NULL;

DROP INDEX IF EXISTS index_appeals_on_rejected_by_account_id;
CREATE INDEX index_appeals_on_rejected_by_account_id ON appeals (rejected_by_account_id) WHERE rejected_by_account_id IS NOT NULL;

DROP INDEX IF EXISTS index_bulk_imports_unconfirmed;
CREATE INDEX index_bulk_imports_unconfirmed ON bulk_imports (id) WHERE state = 0;

DROP INDEX IF EXISTS index_conversations_on_parent_status_id;
CREATE UNIQUE INDEX index_conversations_on_parent_status_id ON conversations (parent_status_id) WHERE parent_status_id IS NOT NULL;

DROP INDEX IF EXISTS index_conversations_on_uri;
CREATE UNIQUE INDEX index_conversations_on_uri ON conversations (uri text_pattern_ops) WHERE uri IS NOT NULL;

DROP INDEX IF EXISTS index_list_accounts_on_follow_id;
CREATE INDEX index_list_accounts_on_follow_id ON list_accounts (follow_id) WHERE follow_id IS NOT NULL;

DROP INDEX IF EXISTS index_list_accounts_on_follow_request_id;
CREATE INDEX index_list_accounts_on_follow_request_id ON list_accounts (follow_request_id) WHERE follow_request_id IS NOT NULL;

DROP INDEX IF EXISTS index_media_attachments_on_scheduled_status_id;
CREATE INDEX index_media_attachments_on_scheduled_status_id ON media_attachments (scheduled_status_id) WHERE scheduled_status_id IS NOT NULL;

DROP INDEX IF EXISTS index_media_attachments_on_shortcode;
CREATE UNIQUE INDEX index_media_attachments_on_shortcode ON media_attachments (shortcode text_pattern_ops) WHERE shortcode IS NOT NULL;

DROP INDEX IF EXISTS index_notifications_on_account_id_and_group_key;
CREATE INDEX index_notifications_on_account_id_and_group_key ON notifications (account_id, group_key) WHERE group_key IS NOT NULL;

DROP INDEX IF EXISTS index_notifications_on_filtered;
CREATE INDEX index_notifications_on_filtered ON notifications (account_id, id DESC, type) WHERE filtered = false;

DROP INDEX IF EXISTS index_oauth_access_tokens_on_refresh_token;
CREATE UNIQUE INDEX index_oauth_access_tokens_on_refresh_token ON oauth_access_tokens (refresh_token) WHERE refresh_token IS NOT NULL;

DROP INDEX IF EXISTS index_oauth_access_tokens_on_resource_owner_id;
CREATE INDEX index_oauth_access_tokens_on_resource_owner_id ON oauth_access_tokens (resource_owner_id) WHERE resource_owner_id IS NOT NULL;

DROP INDEX IF EXISTS index_oauth_applications_on_superapp;
CREATE INDEX index_oauth_applications_on_superapp ON oauth_applications (superapp) WHERE superapp = true;

DROP INDEX IF EXISTS index_polls_on_status_id;
CREATE UNIQUE INDEX index_polls_on_status_id ON polls (status_id);

DROP INDEX IF EXISTS index_preview_cards_on_author_account_id;
CREATE INDEX index_preview_cards_on_author_account_id ON preview_cards (author_account_id) WHERE author_account_id IS NOT NULL;

DROP INDEX IF EXISTS index_quotes_on_activity_uri;
CREATE UNIQUE INDEX index_quotes_on_activity_uri ON quotes (activity_uri) WHERE activity_uri IS NOT NULL;

DROP INDEX IF EXISTS index_quotes_on_approval_uri;
CREATE INDEX index_quotes_on_approval_uri ON quotes (approval_uri) WHERE approval_uri IS NOT NULL;

DROP INDEX IF EXISTS index_reports_on_action_taken_by_account_id;
CREATE INDEX index_reports_on_action_taken_by_account_id ON reports (action_taken_by_account_id) WHERE action_taken_by_account_id IS NOT NULL;

DROP INDEX IF EXISTS index_reports_on_assigned_account_id;
CREATE INDEX index_reports_on_assigned_account_id ON reports (assigned_account_id);

DROP INDEX IF EXISTS index_statuses_20190820;
CREATE INDEX index_statuses_20190820 ON statuses (account_id, id DESC, visibility, updated_at) WHERE deleted_at IS NULL;

DROP INDEX IF EXISTS index_statuses_on_deleted_at;
CREATE INDEX index_statuses_on_deleted_at ON statuses (deleted_at) WHERE deleted_at IS NOT NULL;

DROP INDEX IF EXISTS index_statuses_on_in_reply_to_account_id;
CREATE INDEX index_statuses_on_in_reply_to_account_id ON statuses (in_reply_to_account_id) WHERE in_reply_to_account_id IS NOT NULL;

DROP INDEX IF EXISTS index_statuses_on_in_reply_to_id;
CREATE INDEX index_statuses_on_in_reply_to_id ON statuses (in_reply_to_id) WHERE in_reply_to_id IS NOT NULL;

DROP INDEX IF EXISTS index_statuses_on_uri;
CREATE UNIQUE INDEX index_statuses_on_uri ON statuses (uri text_pattern_ops) WHERE uri IS NOT NULL;

DROP INDEX IF EXISTS index_tags_on_name_lower_btree;
CREATE UNIQUE INDEX index_tags_on_name_lower_btree ON tags (lower(name) text_pattern_ops);

DROP INDEX IF EXISTS index_terms_of_services_on_effective_date;
CREATE UNIQUE INDEX index_terms_of_services_on_effective_date ON terms_of_services (effective_date) WHERE effective_date IS NOT NULL;

DROP INDEX IF EXISTS index_username_blocks_on_username_lower_btree;
CREATE UNIQUE INDEX index_username_blocks_on_username_lower_btree ON username_blocks (lower(username));

DROP INDEX IF EXISTS index_users_on_created_by_application_id;
CREATE INDEX index_users_on_created_by_application_id ON users (created_by_application_id) WHERE created_by_application_id IS NOT NULL;

DROP INDEX IF EXISTS index_users_on_reset_password_token;
CREATE UNIQUE INDEX index_users_on_reset_password_token ON users (reset_password_token) WHERE reset_password_token IS NOT NULL;

DROP INDEX IF EXISTS index_users_on_role_id;
CREATE INDEX index_users_on_role_id ON users (role_id) WHERE role_id IS NOT NULL;

DROP INDEX IF EXISTS index_users_on_unconfirmed_email;
CREATE INDEX index_users_on_unconfirmed_email ON users (unconfirmed_email) WHERE unconfirmed_email IS NOT NULL;

DROP INDEX IF EXISTS index_web_push_subscriptions_on_access_token_id;
CREATE INDEX index_web_push_subscriptions_on_access_token_id ON web_push_subscriptions (access_token_id);
