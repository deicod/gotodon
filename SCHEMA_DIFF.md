# Schema Diff: Mastodon 4.5.3 vs Gotodon

Generated from `mastodon/db/schema.rb` vs `db/migrations/*.sql`.

## Summary
- Mastodon tables: 107
- Gotodon tables: 108
- Missing in Gotodon: 0
- Extra in Gotodon: 1

## Missing Tables in Gotodon

## Extra Tables in Gotodon (not in Mastodon schema)
- preferences

## Column Diffs (Shared Tables)
### account_aliases
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### account_conversations
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### account_deletion_requests
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### account_domain_blocks
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### account_migrations
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### account_moderation_notes
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### account_notes
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### account_pins
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### account_relationship_severance_events
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### account_stats
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### account_statuses_cleanup_policies
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### account_warning_presets
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### account_warnings
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### admin_action_logs
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### announcement_mutes
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### announcement_reactions
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### announcements
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### annual_report_statuses_per_account_counts
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### appeals
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### backups
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### blocks
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### bookmarks
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### bulk_import_rows
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### bulk_imports
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### canonical_email_blocks
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### conversation_mutes
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### conversations
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### custom_emoji_categories
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### custom_emojis
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### custom_filter_keywords
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### custom_filter_statuses
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### custom_filters
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### domain_allows
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### domain_blocks
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### email_domain_blocks
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### fasp_backfill_requests
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### fasp_debug_callbacks
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### fasp_follow_recommendations
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### fasp_providers
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### fasp_subscriptions
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### favourites
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### featured_tags
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### follow_recommendation_mutes
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### follow_recommendation_suppressions
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### follow_requests
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### follows
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### generated_annual_reports
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### identities
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### instance_moderation_notes
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### invites
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### ip_blocks
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### list_accounts
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### lists
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### login_activities
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### markers
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### mentions
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### mutes
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### notification_permissions
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### notification_policies
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### notifications
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### oauth_access_grants
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### oauth_access_tokens
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### oauth_applications
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### pghero_space_stats
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### poll_votes
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### polls
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### preview_card_providers
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### preview_card_trends
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### preview_cards
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### relationship_severance_events
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### relays
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### report_notes
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### reports
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### rule_translations
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### rules
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### scheduled_statuses
### session_activations
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### settings
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### severed_relationships
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### site_uploads
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### software_updates
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### status_edits
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### status_pins
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### status_stats
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### status_trends
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### statuses
### tag_follows
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### tag_trends
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### tags
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### terms_of_services
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### tombstones
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### unavailable_domains
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### user_invite_requests
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### user_roles
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### username_blocks
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### users
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### web_push_subscriptions
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### web_settings
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### webauthn_credentials
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

### webhooks
- Potential type mismatches (normalized):
  - id: rails=bigint | go=bigserial PRIMARY KEY

## Index Diffs (Shared Tables)
### account_aliases
- Missing indexes in Gotodon:
  - cols=account_id,uri|unique=True|opts=, name: "index_account_aliases_on_account_id_and_uri", unique: true
- Extra indexes in Gotodon:
  - cols=account_id,uri|unique=True|opts=

### account_conversations
- Missing indexes in Gotodon:
  - cols=account_id,conversation_id,participant_account_ids|unique=True|opts=, name: "index_unique_conversations", unique: true
  - cols=conversation_id|unique=False|opts=, name: "index_account_conversations_on_conversation_id"
- Extra indexes in Gotodon:
  - cols=account_id,conversation_id,participant_account_ids|unique=True|opts=
  - cols=conversation_id|unique=False|opts=

### account_deletion_requests
- Missing indexes in Gotodon:
  - cols=account_id|unique=False|opts=, name: "index_account_deletion_requests_on_account_id"
- Extra indexes in Gotodon:
  - cols=account_id|unique=False|opts=

### account_domain_blocks
- Extra indexes in Gotodon:
  - cols=account_id,domain|unique=True|opts=

### account_migrations
- Missing indexes in Gotodon:
  - cols=account_id|unique=False|opts=, name: "index_account_migrations_on_account_id"
  - cols=target_account_id|unique=False|opts=, name: "index_account_migrations_on_target_account_id", where: "(target_account_id is not null)"
- Extra indexes in Gotodon:
  - cols=account_id|unique=False|opts=
  - cols=target_account_id|unique=False|opts=where target_account_id is not null

### account_moderation_notes
- Missing indexes in Gotodon:
  - cols=account_id|unique=False|opts=, name: "index_account_moderation_notes_on_account_id"
  - cols=target_account_id|unique=False|opts=, name: "index_account_moderation_notes_on_target_account_id"
- Extra indexes in Gotodon:
  - cols=account_id|unique=False|opts=
  - cols=target_account_id|unique=False|opts=

### account_notes
- Missing indexes in Gotodon:
  - cols=account_id,target_account_id|unique=True|opts=, name: "index_account_notes_on_account_id_and_target_account_id", unique: true
  - cols=target_account_id|unique=False|opts=, name: "index_account_notes_on_target_account_id"
- Extra indexes in Gotodon:
  - cols=account_id,target_account_id|unique=True|opts=
  - cols=target_account_id|unique=False|opts=

### account_pins
- Extra indexes in Gotodon:
  - cols=account_id,target_account_id|unique=True|opts=

### account_relationship_severance_events
- Missing indexes in Gotodon:
  - cols=account_id,relationship_severance_event_id|unique=True|opts=, name: "idx_on_account_id_relationship_severance_event_id_7bd82bf20e", unique: true
  - cols=relationship_severance_event_id|unique=False|opts=, name: "idx_on_relationship_severance_event_id_403f53e707"
- Extra indexes in Gotodon:
  - cols=account_id,relationship_severance_event_id|unique=True|opts=
  - cols=relationship_severance_event_id|unique=False|opts=

### account_stats
- Missing indexes in Gotodon:
  - cols=account_id|unique=True|opts=, name: "index_account_stats_on_account_id", unique: true
- Extra indexes in Gotodon:
  - cols=account_id|unique=True|opts=

### account_statuses_cleanup_policies
- Missing indexes in Gotodon:
  - cols=account_id|unique=False|opts=, name: "index_account_statuses_cleanup_policies_on_account_id"
- Extra indexes in Gotodon:
  - cols=account_id|unique=False|opts=

### account_warnings
- Missing indexes in Gotodon:
  - cols=account_id|unique=False|opts=, name: "index_account_warnings_on_account_id"
  - cols=target_account_id|unique=False|opts=, name: "index_account_warnings_on_target_account_id"
- Extra indexes in Gotodon:
  - cols=account_id|unique=False|opts=
  - cols=target_account_id|unique=False|opts=

### accounts
- Missing indexes in Gotodon:
  - cols=domain,id|unique=False|opts=, name: "index_accounts_on_domain_and_id"
  - cols=lower((username)::text), COALESCE(lower((domain)::text), ''::text)|unique=True|opts=, name: "index_accounts_on_username_and_domain_lower", unique: true
  - cols=uri|unique=False|opts=, name: "index_accounts_on_uri"
  - cols=url|unique=False|opts=, name: "index_accounts_on_url", opclass: :text_pattern_ops, where: "(url is not null)"
- Extra indexes in Gotodon:
  - cols=domain,id|unique=False|opts=
  - cols=uri|unique=False|opts=
  - cols=url text_pattern_ops|unique=False|opts=where url is not null

### accounts_tags
- Missing indexes in Gotodon:
  - cols=account_id,tag_id|unique=False|opts=, name: "index_accounts_tags_on_account_id_and_tag_id"
- Extra indexes in Gotodon:
  - cols=account_id,tag_id|unique=False|opts=

### admin_action_logs
- Missing indexes in Gotodon:
  - cols=account_id|unique=False|opts=, name: "index_admin_action_logs_on_account_id"
  - cols=target_type,target_id|unique=False|opts=, name: "index_admin_action_logs_on_target_type_and_target_id"
- Extra indexes in Gotodon:
  - cols=account_id|unique=False|opts=
  - cols=target_type,target_id|unique=False|opts=

### announcement_mutes
- Extra indexes in Gotodon:
  - cols=account_id,announcement_id|unique=True|opts=

### announcement_reactions
- Missing indexes in Gotodon:
  - cols=announcement_id|unique=False|opts=, name: "index_announcement_reactions_on_announcement_id"
- Extra indexes in Gotodon:
  - cols=account_id,announcement_id,name|unique=True|opts=where account_id is not null
  - cols=announcement_id|unique=False|opts=

### annual_report_statuses_per_account_counts
- Missing indexes in Gotodon:
  - cols=year,account_id|unique=True|opts=, name: "idx_on_year_account_id_ff3e167cef", unique: true
- Extra indexes in Gotodon:
  - cols=year,account_id|unique=True|opts=

### appeals
- Missing indexes in Gotodon:
  - cols=account_id|unique=False|opts=, name: "index_appeals_on_account_id"
  - cols=account_warning_id|unique=True|opts=, name: "index_appeals_on_account_warning_id", unique: true
  - cols=approved_by_account_id|unique=False|opts=, name: "index_appeals_on_approved_by_account_id", where: "(approved_by_account_id is not null)"
  - cols=rejected_by_account_id|unique=False|opts=, name: "index_appeals_on_rejected_by_account_id", where: "(rejected_by_account_id is not null)"
- Extra indexes in Gotodon:
  - cols=account_id|unique=False|opts=
  - cols=account_warning_id|unique=True|opts=
  - cols=approved_by_account_id|unique=False|opts=where approved_by_account_id is not null
  - cols=rejected_by_account_id|unique=False|opts=where rejected_by_account_id is not null

### backups
- Missing indexes in Gotodon:
  - cols=user_id|unique=False|opts=, name: "index_backups_on_user_id"
- Extra indexes in Gotodon:
  - cols=user_id|unique=False|opts=

### blocks
- Missing indexes in Gotodon:
  - cols=account_id,target_account_id|unique=True|opts=, name: "index_blocks_on_account_id_and_target_account_id", unique: true
  - cols=target_account_id|unique=False|opts=, name: "index_blocks_on_target_account_id"
- Extra indexes in Gotodon:
  - cols=account_id,target_account_id|unique=True|opts=
  - cols=target_account_id|unique=False|opts=

### bookmarks
- Missing indexes in Gotodon:
  - cols=account_id,status_id|unique=True|opts=, name: "index_bookmarks_on_account_id_and_status_id", unique: true
  - cols=status_id|unique=False|opts=, name: "index_bookmarks_on_status_id"
- Extra indexes in Gotodon:
  - cols=account_id,status_id|unique=True|opts=
  - cols=status_id|unique=False|opts=

### bulk_import_rows
- Missing indexes in Gotodon:
  - cols=bulk_import_id|unique=False|opts=, name: "index_bulk_import_rows_on_bulk_import_id"
- Extra indexes in Gotodon:
  - cols=bulk_import_id|unique=False|opts=

### bulk_imports
- Missing indexes in Gotodon:
  - cols=account_id|unique=False|opts=, name: "index_bulk_imports_on_account_id"
  - cols=id|unique=False|opts=, name: "index_bulk_imports_unconfirmed", where: "(state = 0)"
- Extra indexes in Gotodon:
  - cols=account_id|unique=False|opts=
  - cols=id|unique=False|opts=where state = 0

### canonical_email_blocks
- Missing indexes in Gotodon:
  - cols=canonical_email_hash|unique=True|opts=, name: "index_canonical_email_blocks_on_canonical_email_hash", unique: true
  - cols=reference_account_id|unique=False|opts=, name: "index_canonical_email_blocks_on_reference_account_id"
- Extra indexes in Gotodon:
  - cols=canonical_email_hash|unique=True|opts=
  - cols=reference_account_id|unique=False|opts=

### conversations
- Missing indexes in Gotodon:
  - cols=uri|unique=True|opts=, name: "index_conversations_on_uri", unique: true, opclass: :text_pattern_ops, where: "(uri is not null)"
- Extra indexes in Gotodon:
  - cols=uri text_pattern_ops|unique=True|opts=where uri is not null

### custom_emoji_categories
- Missing indexes in Gotodon:
  - cols=name|unique=True|opts=, name: "index_custom_emoji_categories_on_name", unique: true
- Extra indexes in Gotodon:
  - cols=name|unique=True|opts=

### custom_emojis
- Extra indexes in Gotodon:
  - cols=domain,id DESC|unique=False|opts=

### custom_filter_keywords
- Missing indexes in Gotodon:
  - cols=custom_filter_id|unique=False|opts=, name: "index_custom_filter_keywords_on_custom_filter_id"
- Extra indexes in Gotodon:
  - cols=custom_filter_id|unique=False|opts=

### custom_filter_statuses
- Missing indexes in Gotodon:
  - cols=custom_filter_id|unique=False|opts=, name: "index_custom_filter_statuses_on_custom_filter_id"
  - cols=status_id,custom_filter_id|unique=True|opts=, name: "index_custom_filter_statuses_on_status_id_and_custom_filter_id", unique: true
- Extra indexes in Gotodon:
  - cols=custom_filter_id|unique=False|opts=
  - cols=status_id,custom_filter_id|unique=True|opts=

### custom_filters
- Missing indexes in Gotodon:
  - cols=account_id|unique=False|opts=, name: "index_custom_filters_on_account_id"
- Extra indexes in Gotodon:
  - cols=account_id|unique=False|opts=

### domain_allows
- Missing indexes in Gotodon:
  - cols=domain|unique=True|opts=, name: "index_domain_allows_on_domain", unique: true
- Extra indexes in Gotodon:
  - cols=domain|unique=True|opts=

### domain_blocks
- Missing indexes in Gotodon:
  - cols=domain|unique=True|opts=, name: "index_domain_blocks_on_domain", unique: true
- Extra indexes in Gotodon:
  - cols=domain|unique=True|opts=

### email_domain_blocks
- Missing indexes in Gotodon:
  - cols=domain|unique=True|opts=, name: "index_email_domain_blocks_on_domain", unique: true
- Extra indexes in Gotodon:
  - cols=domain|unique=True|opts=

### fasp_backfill_requests
- Missing indexes in Gotodon:
  - cols=fasp_provider_id|unique=False|opts=, name: "index_fasp_backfill_requests_on_fasp_provider_id"
- Extra indexes in Gotodon:
  - cols=fasp_provider_id|unique=False|opts=

### fasp_debug_callbacks
- Missing indexes in Gotodon:
  - cols=fasp_provider_id|unique=False|opts=, name: "index_fasp_debug_callbacks_on_fasp_provider_id"
- Extra indexes in Gotodon:
  - cols=fasp_provider_id|unique=False|opts=

### fasp_follow_recommendations
- Missing indexes in Gotodon:
  - cols=recommended_account_id|unique=False|opts=, name: "index_fasp_follow_recommendations_on_recommended_account_id"
  - cols=requesting_account_id|unique=False|opts=, name: "index_fasp_follow_recommendations_on_requesting_account_id"
- Extra indexes in Gotodon:
  - cols=recommended_account_id|unique=False|opts=
  - cols=requesting_account_id|unique=False|opts=

### fasp_providers
- Missing indexes in Gotodon:
  - cols=base_url|unique=True|opts=, name: "index_fasp_providers_on_base_url", unique: true
- Extra indexes in Gotodon:
  - cols=base_url|unique=True|opts=

### fasp_subscriptions
- Missing indexes in Gotodon:
  - cols=fasp_provider_id|unique=False|opts=, name: "index_fasp_subscriptions_on_fasp_provider_id"
- Extra indexes in Gotodon:
  - cols=fasp_provider_id|unique=False|opts=

### favourites
- Missing indexes in Gotodon:
  - cols=account_id,status_id|unique=True|opts=, name: "index_favourites_on_account_id_and_status_id", unique: true
  - cols=status_id|unique=False|opts=, name: "index_favourites_on_status_id"
- Extra indexes in Gotodon:
  - cols=account_id,status_id|unique=True|opts=
  - cols=status_id|unique=False|opts=

### featured_tags
- Extra indexes in Gotodon:
  - cols=account_id,name|unique=True|opts=

### follow_recommendation_mutes
- Missing indexes in Gotodon:
  - cols=account_id,target_account_id|unique=True|opts=, name: "idx_on_account_id_target_account_id_a8c8ddf44e", unique: true
  - cols=target_account_id|unique=False|opts=, name: "index_follow_recommendation_mutes_on_target_account_id"
- Extra indexes in Gotodon:
  - cols=account_id,target_account_id|unique=True|opts=
  - cols=target_account_id|unique=False|opts=

### follow_recommendation_suppressions
- Missing indexes in Gotodon:
  - cols=account_id|unique=True|opts=, name: "index_follow_recommendation_suppressions_on_account_id", unique: true
- Extra indexes in Gotodon:
  - cols=account_id|unique=True|opts=

### follow_requests
- Missing indexes in Gotodon:
  - cols=account_id,target_account_id|unique=True|opts=, name: "index_follow_requests_on_account_id_and_target_account_id", unique: true
- Extra indexes in Gotodon:
  - cols=account_id,target_account_id|unique=True|opts=

### follows
- Missing indexes in Gotodon:
  - cols=account_id,target_account_id|unique=True|opts=, name: "index_follows_on_account_id_and_target_account_id", unique: true
  - cols=target_account_id,account_id|unique=False|opts=, name: "index_follows_on_target_account_id_and_account_id"
- Extra indexes in Gotodon:
  - cols=account_id,target_account_id|unique=True|opts=
  - cols=target_account_id,account_id|unique=False|opts=

### generated_annual_reports
- Missing indexes in Gotodon:
  - cols=account_id,year|unique=True|opts=, name: "index_generated_annual_reports_on_account_id_and_year", unique: true
- Extra indexes in Gotodon:
  - cols=account_id,year|unique=True|opts=

### identities
- Missing indexes in Gotodon:
  - cols=uid,provider|unique=True|opts=, name: "index_identities_on_uid_and_provider", unique: true
  - cols=user_id|unique=False|opts=, name: "index_identities_on_user_id"
- Extra indexes in Gotodon:
  - cols=uid,provider|unique=True|opts=
  - cols=user_id|unique=False|opts=

### instance_moderation_notes
- Missing indexes in Gotodon:
  - cols=domain|unique=False|opts=, name: "index_instance_moderation_notes_on_domain"
- Extra indexes in Gotodon:
  - cols=domain|unique=False|opts=

### invites
- Missing indexes in Gotodon:
  - cols=code|unique=True|opts=, name: "index_invites_on_code", unique: true
  - cols=user_id|unique=False|opts=, name: "index_invites_on_user_id"
- Extra indexes in Gotodon:
  - cols=code|unique=True|opts=
  - cols=user_id|unique=False|opts=

### ip_blocks
- Missing indexes in Gotodon:
  - cols=ip|unique=True|opts=, name: "index_ip_blocks_on_ip", unique: true
- Extra indexes in Gotodon:
  - cols=ip|unique=True|opts=

### lists
- Missing indexes in Gotodon:
  - cols=account_id|unique=False|opts=, name: "index_lists_on_account_id"
- Extra indexes in Gotodon:
  - cols=account_id|unique=False|opts=

### login_activities
- Missing indexes in Gotodon:
  - cols=user_id|unique=False|opts=, name: "index_login_activities_on_user_id"
- Extra indexes in Gotodon:
  - cols=user_id|unique=False|opts=

### markers
- Extra indexes in Gotodon:
  - cols=user_id,timeline|unique=True|opts=

### media_attachments
- Missing indexes in Gotodon:
  - cols=account_id,status_id|unique=False|opts=, name: "index_media_attachments_on_account_id_and_status_id", order: { status_id: :desc }
  - cols=shortcode|unique=True|opts=, name: "index_media_attachments_on_shortcode", unique: true, opclass: :text_pattern_ops, where: "(shortcode is not null)"
  - cols=status_id|unique=False|opts=, name: "index_media_attachments_on_status_id"
- Extra indexes in Gotodon:
  - cols=account_id,status_id DESC|unique=False|opts=
  - cols=shortcode text_pattern_ops|unique=True|opts=where shortcode is not null
  - cols=status_id|unique=False|opts=

### mentions
- Missing indexes in Gotodon:
  - cols=account_id,status_id|unique=True|opts=, name: "index_mentions_on_account_id_and_status_id", unique: true
  - cols=status_id|unique=False|opts=, name: "index_mentions_on_status_id"
- Extra indexes in Gotodon:
  - cols=account_id,status_id|unique=True|opts=
  - cols=status_id|unique=False|opts=

### mutes
- Missing indexes in Gotodon:
  - cols=account_id,target_account_id|unique=True|opts=, name: "index_mutes_on_account_id_and_target_account_id", unique: true
  - cols=target_account_id|unique=False|opts=, name: "index_mutes_on_target_account_id"
- Extra indexes in Gotodon:
  - cols=account_id,target_account_id|unique=True|opts=
  - cols=target_account_id|unique=False|opts=

### notification_permissions
- Missing indexes in Gotodon:
  - cols=account_id|unique=False|opts=, name: "index_notification_permissions_on_account_id"
  - cols=from_account_id|unique=False|opts=, name: "index_notification_permissions_on_from_account_id"
- Extra indexes in Gotodon:
  - cols=account_id,from_account_id|unique=True|opts=
  - cols=account_id|unique=False|opts=
  - cols=from_account_id|unique=False|opts=

### notification_policies
- Missing indexes in Gotodon:
  - cols=account_id|unique=True|opts=, name: "index_notification_policies_on_account_id", unique: true
- Extra indexes in Gotodon:
  - cols=account_id|unique=True|opts=

### notification_requests
- Missing indexes in Gotodon:
  - cols=account_id,from_account_id|unique=True|opts=, name: "index_notification_requests_on_account_id_and_from_account_id", unique: true
  - cols=from_account_id|unique=False|opts=, name: "index_notification_requests_on_from_account_id"
  - cols=last_status_id|unique=False|opts=, name: "index_notification_requests_on_last_status_id"
- Extra indexes in Gotodon:
  - cols=account_id,from_account_id|unique=True|opts=
  - cols=from_account_id|unique=False|opts=
  - cols=last_status_id|unique=False|opts=

### notifications
- Missing indexes in Gotodon:
  - cols=account_id,group_key|unique=False|opts=, name: "index_notifications_on_account_id_and_group_key", where: "(group_key is not null)"
  - cols=account_id,id,type|unique=False|opts=, name: "index_notifications_on_account_id_and_id_and_type", order: { id: :desc }
  - cols=activity_id,activity_type|unique=False|opts=, name: "index_notifications_on_activity_id_and_activity_type"
  - cols=from_account_id|unique=False|opts=, name: "index_notifications_on_from_account_id"
- Extra indexes in Gotodon:
  - cols=account_id,id DESC,type|unique=False|opts=
  - cols=activity_id,activity_type|unique=False|opts=
  - cols=from_account_id|unique=False|opts=

### oauth_access_grants
- Missing indexes in Gotodon:
  - cols=resource_owner_id|unique=False|opts=, name: "index_oauth_access_grants_on_resource_owner_id"
  - cols=token|unique=True|opts=, name: "index_oauth_access_grants_on_token", unique: true
- Extra indexes in Gotodon:
  - cols=resource_owner_id|unique=False|opts=
  - cols=token|unique=True|opts=

### oauth_access_tokens
- Missing indexes in Gotodon:
  - cols=refresh_token|unique=True|opts=, name: "index_oauth_access_tokens_on_refresh_token", unique: true, opclass: :text_pattern_ops, where: "(refresh_token is not null)"
  - cols=resource_owner_id|unique=False|opts=, name: "index_oauth_access_tokens_on_resource_owner_id", where: "(resource_owner_id is not null)"
  - cols=token|unique=True|opts=, name: "index_oauth_access_tokens_on_token", unique: true
- Extra indexes in Gotodon:
  - cols=refresh_token|unique=True|opts=where refresh_token is not null
  - cols=resource_owner_id|unique=False|opts=where resource_owner_id is not null
  - cols=token|unique=True|opts=

### oauth_applications
- Missing indexes in Gotodon:
  - cols=uid|unique=True|opts=, name: "index_oauth_applications_on_uid", unique: true
- Extra indexes in Gotodon:
  - cols=uid|unique=True|opts=

### pghero_space_stats
- Missing indexes in Gotodon:
  - cols=database,captured_at|unique=False|opts=, name: "index_pghero_space_stats_on_database_and_captured_at"
- Extra indexes in Gotodon:
  - cols=database,captured_at|unique=False|opts=

### poll_votes
- Missing indexes in Gotodon:
  - cols=poll_id|unique=False|opts=, name: "index_poll_votes_on_poll_id"
- Extra indexes in Gotodon:
  - cols=poll_id,account_id,choice|unique=True|opts=
  - cols=poll_id|unique=False|opts=

### polls
- Missing indexes in Gotodon:
  - cols=status_id|unique=False|opts=, name: "index_polls_on_status_id"
- Extra indexes in Gotodon:
  - cols=status_id|unique=True|opts=

### preview_card_providers
- Missing indexes in Gotodon:
  - cols=domain|unique=True|opts=, name: "index_preview_card_providers_on_domain", unique: true
- Extra indexes in Gotodon:
  - cols=domain|unique=True|opts=

### preview_card_trends
- Missing indexes in Gotodon:
  - cols=preview_card_id|unique=True|opts=, name: "index_preview_card_trends_on_preview_card_id", unique: true
- Extra indexes in Gotodon:
  - cols=preview_card_id|unique=True|opts=

### preview_cards
- Missing indexes in Gotodon:
  - cols=author_account_id|unique=False|opts=, name: "index_preview_cards_on_author_account_id", where: "(author_account_id is not null)"
  - cols=url|unique=True|opts=, name: "index_preview_cards_on_url", unique: true
- Extra indexes in Gotodon:
  - cols=author_account_id|unique=False|opts=where author_account_id is not null
  - cols=url|unique=True|opts=

### quotes
- Missing indexes in Gotodon:
  - cols=account_id,quoted_account_id,id|unique=False|opts=, name: "index_quotes_on_account_id_and_quoted_account_id_and_id"
  - cols=activity_uri|unique=True|opts=, name: "index_quotes_on_activity_uri", unique: true, where: "(activity_uri is not null)"
  - cols=approval_uri|unique=False|opts=, name: "index_quotes_on_approval_uri", where: "(approval_uri is not null)"
  - cols=quoted_account_id|unique=False|opts=, name: "index_quotes_on_quoted_account_id"
  - cols=quoted_status_id,id|unique=False|opts=, name: "index_quotes_on_quoted_status_id_and_id"
  - cols=status_id|unique=True|opts=, name: "index_quotes_on_status_id", unique: true
- Extra indexes in Gotodon:
  - cols=account_id,quoted_account_id,id|unique=False|opts=
  - cols=activity_uri|unique=True|opts=where activity_uri is not null
  - cols=approval_uri|unique=False|opts=where approval_uri is not null
  - cols=quoted_account_id|unique=False|opts=
  - cols=quoted_status_id,id|unique=False|opts=
  - cols=status_id|unique=True|opts=

### relationship_severance_events
- Missing indexes in Gotodon:
  - cols=type,target_name|unique=False|opts=, name: "index_relationship_severance_events_on_type_and_target_name"
- Extra indexes in Gotodon:
  - cols=type,target_name|unique=False|opts=

### report_notes
- Missing indexes in Gotodon:
  - cols=account_id|unique=False|opts=, name: "index_report_notes_on_account_id"
  - cols=report_id|unique=False|opts=, name: "index_report_notes_on_report_id"
- Extra indexes in Gotodon:
  - cols=account_id|unique=False|opts=
  - cols=report_id|unique=False|opts=

### reports
- Missing indexes in Gotodon:
  - cols=account_id|unique=False|opts=, name: "index_reports_on_account_id"
  - cols=assigned_account_id|unique=False|opts=, name: "index_reports_on_assigned_account_id", where: "(assigned_account_id is not null)"
  - cols=target_account_id|unique=False|opts=, name: "index_reports_on_target_account_id"
- Extra indexes in Gotodon:
  - cols=account_id|unique=False|opts=
  - cols=assigned_account_id|unique=False|opts=
  - cols=target_account_id|unique=False|opts=

### rule_translations
- Missing indexes in Gotodon:
  - cols=rule_id,language|unique=True|opts=, name: "index_rule_translations_on_rule_id_and_language", unique: true
- Extra indexes in Gotodon:
  - cols=rule_id,language|unique=True|opts=

### scheduled_statuses
- Missing indexes in Gotodon:
  - cols=account_id|unique=False|opts=, name: "index_scheduled_statuses_on_account_id"
  - cols=scheduled_at|unique=False|opts=, name: "index_scheduled_statuses_on_scheduled_at"
- Extra indexes in Gotodon:
  - cols=account_id|unique=False|opts=
  - cols=scheduled_at|unique=False|opts=

### session_activations
- Missing indexes in Gotodon:
  - cols=access_token_id|unique=False|opts=, name: "index_session_activations_on_access_token_id"
  - cols=session_id|unique=True|opts=, name: "index_session_activations_on_session_id", unique: true
  - cols=user_id|unique=False|opts=, name: "index_session_activations_on_user_id"
- Extra indexes in Gotodon:
  - cols=access_token_id|unique=False|opts=
  - cols=session_id|unique=True|opts=
  - cols=user_id|unique=False|opts=

### settings
- Missing indexes in Gotodon:
  - cols=var|unique=True|opts=, name: "index_settings_on_var", unique: true
- Extra indexes in Gotodon:
  - cols=var|unique=True|opts=

### severed_relationships
- Missing indexes in Gotodon:
  - cols=local_account_id,relationship_severance_event_id|unique=False|opts=, name: "index_severed_relationships_on_local_account_and_event"
  - cols=relationship_severance_event_id,local_account_id,direction,remote_account_id|unique=True|opts=, name: "index_severed_relationships_on_unique_tuples", unique: true
  - cols=remote_account_id|unique=False|opts=, name: "index_severed_relationships_on_remote_account_id"
- Extra indexes in Gotodon:
  - cols=local_account_id,relationship_severance_event_id|unique=False|opts=
  - cols=relationship_severance_event_id,local_account_id,direction,remote_account_id|unique=True|opts=
  - cols=remote_account_id|unique=False|opts=

### site_uploads
- Missing indexes in Gotodon:
  - cols=var|unique=True|opts=, name: "index_site_uploads_on_var", unique: true
- Extra indexes in Gotodon:
  - cols=var|unique=True|opts=

### software_updates
- Missing indexes in Gotodon:
  - cols=version|unique=True|opts=, name: "index_software_updates_on_version", unique: true
- Extra indexes in Gotodon:
  - cols=version|unique=True|opts=

### status_edits
- Missing indexes in Gotodon:
  - cols=account_id|unique=False|opts=, name: "index_status_edits_on_account_id"
  - cols=status_id|unique=False|opts=, name: "index_status_edits_on_status_id"
- Extra indexes in Gotodon:
  - cols=account_id|unique=False|opts=
  - cols=status_id|unique=False|opts=

### status_pins
- Missing indexes in Gotodon:
  - cols=status_id|unique=False|opts=, name: "index_status_pins_on_status_id"
- Extra indexes in Gotodon:
  - cols=account_id,status_id|unique=True|opts=
  - cols=status_id|unique=False|opts=

### status_stats
- Missing indexes in Gotodon:
  - cols=status_id|unique=True|opts=, name: "index_status_stats_on_status_id", unique: true
- Extra indexes in Gotodon:
  - cols=status_id|unique=True|opts=

### status_trends
- Missing indexes in Gotodon:
  - cols=account_id|unique=False|opts=, name: "index_status_trends_on_account_id"
  - cols=status_id|unique=True|opts=, name: "index_status_trends_on_status_id", unique: true
- Extra indexes in Gotodon:
  - cols=account_id|unique=False|opts=
  - cols=status_id|unique=True|opts=

### statuses
- Missing indexes in Gotodon:
  - cols=account_id|unique=False|opts=, name: "index_statuses_on_account_id"
  - cols=conversation_id|unique=False|opts=, name: "index_statuses_on_conversation_id"
  - cols=in_reply_to_id|unique=False|opts=, name: "index_statuses_on_in_reply_to_id", where: "(in_reply_to_id is not null)"
  - cols=reblog_of_id,account_id|unique=False|opts=, name: "index_statuses_on_reblog_of_id_and_account_id"
  - cols=uri|unique=True|opts=, name: "index_statuses_on_uri", unique: true, opclass: :text_pattern_ops, where: "(uri is not null)"
- Extra indexes in Gotodon:
  - cols=account_id|unique=False|opts=
  - cols=conversation_id|unique=False|opts=
  - cols=in_reply_to_id|unique=False|opts=where in_reply_to_id is not null
  - cols=reblog_of_id,account_id|unique=False|opts=
  - cols=uri text_pattern_ops|unique=True|opts=where uri is not null

### statuses_tags
- Missing indexes in Gotodon:
  - cols=status_id|unique=False|opts=, name: "index_statuses_tags_on_status_id"
- Extra indexes in Gotodon:
  - cols=status_id|unique=False|opts=

### tag_follows
- Missing indexes in Gotodon:
  - cols=account_id,tag_id|unique=True|opts=, name: "index_tag_follows_on_account_id_and_tag_id", unique: true
  - cols=tag_id|unique=False|opts=, name: "index_tag_follows_on_tag_id"
- Extra indexes in Gotodon:
  - cols=account_id,tag_id|unique=True|opts=
  - cols=tag_id|unique=False|opts=

### tag_trends
- Missing indexes in Gotodon:
  - cols=tag_id,language|unique=True|opts=, name: "index_tag_trends_on_tag_id_and_language", unique: true
- Extra indexes in Gotodon:
  - cols=tag_id,language|unique=True|opts=

### tags
- Missing indexes in Gotodon:
  - cols=lower((name)::text) text_pattern_ops|unique=True|opts=, name: "index_tags_on_name_lower_btree", unique: true

### terms_of_services
- Missing indexes in Gotodon:
  - cols=effective_date|unique=True|opts=, name: "index_terms_of_services_on_effective_date", unique: true, where: "(effective_date is not null)"
- Extra indexes in Gotodon:
  - cols=effective_date|unique=True|opts=where effective_date is not null

### tombstones
- Missing indexes in Gotodon:
  - cols=account_id|unique=False|opts=, name: "index_tombstones_on_account_id"
  - cols=uri|unique=False|opts=, name: "index_tombstones_on_uri"
- Extra indexes in Gotodon:
  - cols=account_id|unique=False|opts=
  - cols=uri|unique=False|opts=

### unavailable_domains
- Missing indexes in Gotodon:
  - cols=domain|unique=True|opts=, name: "index_unavailable_domains_on_domain", unique: true
- Extra indexes in Gotodon:
  - cols=domain|unique=True|opts=

### user_invite_requests
- Missing indexes in Gotodon:
  - cols=user_id|unique=False|opts=, name: "index_user_invite_requests_on_user_id"
- Extra indexes in Gotodon:
  - cols=user_id|unique=False|opts=

### username_blocks
- Missing indexes in Gotodon:
  - cols=lower((username)::text)|unique=True|opts=, name: "index_username_blocks_on_username_lower_btree", unique: true
  - cols=normalized_username|unique=False|opts=, name: "index_username_blocks_on_normalized_username"
- Extra indexes in Gotodon:
  - cols=normalized_username|unique=False|opts=

### users
- Missing indexes in Gotodon:
  - cols=account_id|unique=False|opts=, name: "index_users_on_account_id"
  - cols=confirmation_token|unique=True|opts=, name: "index_users_on_confirmation_token", unique: true
  - cols=email|unique=True|opts=, name: "index_users_on_email", unique: true
  - cols=reset_password_token|unique=True|opts=, name: "index_users_on_reset_password_token", unique: true, opclass: :text_pattern_ops, where: "(reset_password_token is not null)"
- Extra indexes in Gotodon:
  - cols=account_id|unique=False|opts=
  - cols=confirmation_token|unique=True|opts=
  - cols=email|unique=True|opts=
  - cols=reset_password_token|unique=True|opts=where reset_password_token is not null

### web_push_subscriptions
- Missing indexes in Gotodon:
  - cols=access_token_id|unique=False|opts=, name: "index_web_push_subscriptions_on_access_token_id", where: "(access_token_id is not null)"
  - cols=user_id|unique=False|opts=, name: "index_web_push_subscriptions_on_user_id"
- Extra indexes in Gotodon:
  - cols=access_token_id|unique=False|opts=
  - cols=user_id|unique=False|opts=

### web_settings
- Missing indexes in Gotodon:
  - cols=user_id|unique=True|opts=, name: "index_web_settings_on_user_id", unique: true
- Extra indexes in Gotodon:
  - cols=user_id|unique=True|opts=

### webauthn_credentials
- Missing indexes in Gotodon:
  - cols=external_id|unique=True|opts=, name: "index_webauthn_credentials_on_external_id", unique: true
  - cols=user_id,nickname|unique=True|opts=, name: "index_webauthn_credentials_on_user_id_and_nickname", unique: true
- Extra indexes in Gotodon:
  - cols=external_id|unique=True|opts=
  - cols=user_id,nickname|unique=True|opts=

### webhooks
- Missing indexes in Gotodon:
  - cols=url|unique=True|opts=, name: "index_webhooks_on_url", unique: true
- Extra indexes in Gotodon:
  - cols=url|unique=True|opts=
