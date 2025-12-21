# Gotodon v4.5.3 Compatibility Plan

> **Instructions:** Check off tasks with `[x]` as they are completed. This file tracks progress toward 100% Mastodon v4.5.3 API compatibility.

**Last Updated:** 2025-12-18  
**Target:** Mastodon v4.5.3  
**Estimated Effort:** 4-6 weeks

---

## Milestone 1: Database Schema Alignment

**Goal:** Add all missing tables to match Mastodon v4.5.3 schema

### Task 1.1: Core Content Tables
- [x] Create migration `002_core_content_tables.sql`
- [x] Add `status_edits` table (post edit history)
  - [x] Define columns: status_id, account_id, text, spoiler_text, ordered_media_attachment_ids, etc.
  - [x] Add indexes on status_id, account_id
  - [x] Add foreign keys
- [x] Add `quotes` table (quote posts - v4.5+ feature)
  - [x] Define columns: account_id, status_id, quoted_status_id, quoted_account_id, state, etc.
  - [x] Add indexes
  - [x] Add foreign keys
- [x] Add `preview_cards` table (link previews)
  - [x] Define columns: url, title, description, image, type, author_account_id, etc.
  - [x] Add `preview_cards_statuses` join table
  - [x] Add `preview_card_trends` table
  - [x] Add `preview_card_providers` table

### Task 1.2: Notification System Tables
- [x] Add `notification_policies` table
  - [x] Define columns: account_id, for_not_following, for_not_followers, for_new_accounts, etc.
  - [x] Add unique index on account_id
- [x] Add `notification_requests` table
  - [x] Define columns: account_id, from_account_id, last_status_id, notifications_count
  - [x] Add indexes
- [x] Add `notification_permissions` table
  - [x] Define columns: account_id, from_account_id
  - [x] Add indexes

### Task 1.3: Tag & Trends Tables
- [x] Add `tag_follows` table
  - [x] Define columns: tag_id, account_id
  - [x] Add unique index on account_id + tag_id
- [x] Add `tag_trends` table
  - [x] Define columns: tag_id, score, rank, allowed, language
- [x] Add `status_trends` table
  - [x] Define columns: status_id, account_id, score, rank, allowed, language

### Task 1.4: Account Feature Tables
- [x] Add `account_notes` table (private notes about users)
  - [x] Define columns: account_id, target_account_id, comment
  - [x] Add unique index
- [x] Add `account_aliases` table
  - [x] Define columns: account_id, acct, uri
- [x] Add `account_migrations` table
  - [x] Define columns: account_id, acct, followers_count, target_account_id

### Task 1.5: Admin & Moderation Tables
- [x] Add `rules` table (server rules)
  - [x] Define columns: priority, text, hint, deleted_at
  - [x] Add `rule_translations` table
- [x] Add `domain_allows` table
  - [x] Define columns: domain
- [x] Add `tombstones` table
  - [x] Define columns: account_id, uri, by_moderator

### Task 1.6: Session & Security Tables
- [x] Add `session_activations` table
- [x] Add `login_activities` table
- [x] Add `webauthn_credentials` table

### Task 1.7: Reports & Annual Features
- [x] Add `generated_annual_reports` table
  - [x] Define columns: account_id, year, data (jsonb), schema_version, viewed_at

### Task 1.8: Generate SQL Queries
- [x] Run `sqlc generate` after migration
- [x] Create `db/queries/status_edits.sql`
- [x] Create `db/queries/quotes.sql`
- [x] Create `db/queries/notification_policies.sql`
- [x] Create `db/queries/tag_follows.sql`
- [x] Create `db/queries/preview_cards.sql`
- [x] Create `db/queries/account_notes.sql`
- [x] Create `db/queries/rules.sql`
- [x] Create `db/queries/sessions.sql`
- [x] Create `db/queries/annual_reports.sql`
- [x] Verify with `go build ./...`

---

## Milestone 2: v1 API Endpoint Gaps

**Goal:** Implement all missing v1 API endpoints

### Task 2.1: Status Edit History
- [x] Create `internal/api/v1/status_edits.go`
- [x] Implement `GET /api/v1/statuses/{id}/history`
  - [x] Query status_edits table
  - [x] Serialize to Mastodon format
- [x] Implement `GET /api/v1/statuses/{id}/source`
  - [x] Return raw text and spoiler_text
- [x] Add tests in `status_edits_test.go`

### Task 2.2: Status Translation
- [x] Implement `POST /api/v1/statuses/{id}/translate`
  - [x] Create translation service interface (`internal/translation/service.go`)
  - [x] Integrate DeepL and LibreTranslate backends
  - [x] Add TranslationConfig to config.go
  - [x] Return translated content
- [x] Add tests in `status_edits_test.go`

### Task 2.3: Quote Posts (v4.5+ Feature)
- [x] Add quote support to status creation (QuoteID field added to Status struct)
- [x] Implement `GET /api/v1/statuses/{id}/quotes`
- [x] Implement `POST /api/v1/statuses/{id}/quotes/{quote_id}/revoke`
- [x] Add quote_id and quote to status response serializer
- [x] Add tests in `status_edits_test.go`

### Task 2.4: Instance Information Endpoints
- [x] Create `internal/api/v1/instance_info.go`
- [x] Implement `GET /api/v1/instance/peers`
  - [x] Return list of known domains
- [x] Implement `GET /api/v1/instance/activity`
  - [x] Return weekly activity stats
- [x] Implement `GET /api/v1/instance/rules`
  - [x] Query rules table
- [x] Implement `GET /api/v1/instance/privacy_policy`
- [x] Implement `GET /api/v1/instance/terms_of_service`
- [x] Implement `GET /api/v1/instance/extended_description`
- [x] Implement `GET /api/v1/instance/translation_languages`
- [x] Implement `GET /api/v1/instance/languages`
- [x] Add tests in `instance_info_test.go`

### Task 2.5: Tag Following
- [x] Create `internal/api/v1/tags.go`
- [x] Implement `GET /api/v1/tags/{id}`
  - [x] Return tag info with following status
- [x] Implement `POST /api/v1/tags/{id}/follow`
  - [x] Insert into tag_follows
- [x] Implement `POST /api/v1/tags/{id}/unfollow`
  - [x] Delete from tag_follows
- [x] Implement `POST /api/v1/tags/{id}/feature`
- [x] Implement `POST /api/v1/tags/{id}/unfeature`
- [x] Implement `GET /api/v1/followed_tags`
  - [x] List user's followed tags
- [x] Add tests in `tags_test.go`

### Task 2.6: Notification Improvements
- [x] Implement `GET /api/v1/notifications/unread_count`
- [x] Implement `GET /api/v1/notifications/requests`
  - [x] List notification permission requests
- [x] Implement `POST /api/v1/notifications/requests/{id}/accept`
- [x] Implement `POST /api/v1/notifications/requests/{id}/dismiss`
- [x] Implement `GET /api/v1/notifications/policy`
- [x] Implement `PUT /api/v1/notifications/policy`
- [x] Add tests in `notifications_test.go`

### Task 2.7: Account Notes
- [x] Implement `POST /api/v1/accounts/{id}/note`
  - [x] Create/update private note
- [x] Include note in relationships response
- [x] Add tests in `accounts_notes_test.go`

### Task 2.8: Account Management
- [x] Implement `POST /api/v1/accounts/{id}/remove_from_followers`
- [x] Verify `GET /api/v1/accounts/lookup` works correctly
- [x] Add tests

### Task 2.9: Profile Management
- [x] Implement `DELETE /api/v1/profile/avatar`
- [x] Implement `DELETE /api/v1/profile/header`
- [x] Add tests in `profile_test.go`

### Task 2.10: Annual Reports
- [x] Create `internal/api/v1/annual_reports.go`
- [x] Implement `GET /api/v1/annual_reports`
- [x] Implement `GET /api/v1/annual_reports/{id}`
- [x] Implement `POST /api/v1/annual_reports/{id}/read`
- [x] Add tests in `annual_reports_test.go`

### Task 2.11: Email Confirmations
- [x] Implement `POST /api/v1/emails/confirmations`
- [x] Implement `GET /api/v1/emails/check_confirmation`
- [x] Add tests in `emails_test.go`

### Task 2.12: Peers Search
- [x] Implement `GET /api/v1/peers/search`
- [x] Add tests in `peers_test.go`

---

## Milestone 3: v2 API Endpoint Gaps

**Goal:** Implement all missing v2 API endpoints

### Task 3.1: Grouped Notifications
- [x] Create `internal/api/v2/notifications.go`
- [x] Implement `GET /api/v2/notifications` (grouped by group_key)
  - [x] Group notifications by type and source
  - [x] Return grouped format
- [x] Implement `GET /api/v2/notifications/{group_key}`
- [x] Implement `POST /api/v2/notifications/{group_key}/dismiss`
- [x] Implement `GET /api/v2/notifications/{group_key}/accounts`
- [x] Implement `GET /api/v2/notifications/unread_count`
- [x] Add tests

### Task 3.2: v2 Notification Policy
- [x] Implement `GET /api/v2/notifications/policy`
- [x] Implement `PUT /api/v2/notifications/policy`

---

## Milestone 4: Admin API Gaps

**Goal:** Complete admin API implementation

### Task 4.1: Domain Allows
- [x] Create `internal/api/admin/domain_allows.go`
- [x] Implement `GET /api/v1/admin/domain_allows`
- [x] Implement `GET /api/v1/admin/domain_allows/{id}`
- [x] Implement `POST /api/v1/admin/domain_allows`
- [x] Implement `DELETE /api/v1/admin/domain_allows/{id}`

### Task 4.2: IP Blocks
- [x] Create `internal/api/admin/ip_blocks.go`
- [x] Implement CRUD for `/api/v1/admin/ip_blocks`

### Task 4.3: Canonical Email Blocks
- [x] Create `internal/api/admin/canonical_email_blocks.go`
- [x] Implement CRUD for `/api/v1/admin/canonical_email_blocks`
- [x] Implement `POST /api/v1/admin/canonical_email_blocks/test`

### Task 4.4: Admin Tags
- [ ] Implement `GET /api/v1/admin/tags`
- [ ] Implement `GET /api/v1/admin/tags/{id}`
- [ ] Implement `PUT /api/v1/admin/tags/{id}`

### Task 4.5: Trends Moderation
- [ ] Implement approve/reject for tags
- [ ] Implement approve/reject for links
- [ ] Implement approve/reject for statuses

### Task 4.6: Account Deletion
- [ ] Implement `DELETE /api/v1/admin/accounts/{id}`

---

## Milestone 5: ActivityPub Federation 🔴 CRITICAL

**Goal:** Full federation with other ActivityPub servers

### Task 5.1: HTTP Signature Verification
- [ ] Wire up existing `pkg/activitypub/httpsig.go`
- [ ] Add signature verification middleware
- [ ] Verify signatures on incoming POST requests to inbox
- [ ] Handle signature verification failures gracefully

### Task 5.2: Inbox Activity Routing
- [ ] Modify `PostInbox` in `handler.go`
- [ ] Parse incoming JSON-LD activities
- [ ] Route to appropriate handler based on activity type
- [ ] Return proper HTTP status codes

### Task 5.3: Follow Handling
- [ ] Create `internal/federation/follow.go`
- [ ] Process incoming `Follow` activities
  - [ ] Validate actor exists
  - [ ] Check if auto-accept or require approval
  - [ ] Create follow_request or follow record
- [ ] Send `Accept` activity for approved follows
- [ ] Send `Reject` activity for rejected follows
- [ ] Handle locked accounts (require approval)

### Task 5.4: Accept/Reject Handling
- [ ] Process incoming `Accept` activities
  - [ ] Convert our follow_request to follow
- [ ] Process incoming `Reject` activities
  - [ ] Remove our follow_request

### Task 5.5: Undo Handling
- [ ] Process `Undo Follow` (unfollow)
- [ ] Process `Undo Like` (unfavourite)
- [ ] Process `Undo Announce` (unboost)
- [ ] Process `Undo Block`

### Task 5.6: Create Activity Handling
- [ ] Create `internal/federation/create.go`
- [ ] Process incoming `Create Note` (new posts)
  - [ ] Fetch actor if not known
  - [ ] Parse Note content
  - [ ] Store in statuses table
  - [ ] Process mentions
  - [ ] Process attachments
  - [ ] Generate notifications
- [ ] Handle replies (in_reply_to)
- [ ] Handle visibility (public/unlisted/private/direct)

### Task 5.7: Update Activity Handling
- [ ] Process `Update Note` (post edits)
  - [ ] Create status_edit record
  - [ ] Update status content
- [ ] Process `Update Person` (profile updates)
  - [ ] Update account fields

### Task 5.8: Delete Activity Handling
- [ ] Process `Delete Note`
  - [ ] Mark status as deleted
  - [ ] Create tombstone record
- [ ] Process `Delete Person` (account deletion)

### Task 5.9: Announce Handling (Boosts)
- [ ] Process incoming `Announce` activities
  - [ ] Fetch original post if needed
  - [ ] Create reblog record
  - [ ] Update reblog count
  - [ ] Generate notification

### Task 5.10: Like Handling (Favourites)
- [ ] Process incoming `Like` activities
  - [ ] Create favourite record
  - [ ] Update favourite count
  - [ ] Generate notification

### Task 5.11: Block Handling
- [ ] Process incoming `Block` activities
- [ ] Hide content appropriately

### Task 5.12: Activity Delivery - Signing
- [ ] Create `internal/federation/delivery.go`
- [ ] Implement HTTP signature signing for outgoing requests
- [ ] Use account's private key

### Task 5.13: Activity Delivery - Resolution
- [ ] Resolve recipient inboxes from followers
- [ ] Use shared inbox when available
- [ ] Batch deliveries to same inbox

### Task 5.14: Activity Delivery - Queue
- [ ] Implement delivery queue with retries
- [ ] Handle failed deliveries gracefully
- [ ] Exponential backoff for retries

### Task 5.15: Deliver Outgoing Activities
- [ ] Deliver `Create` for new posts
- [ ] Deliver `Update` for edited posts
- [ ] Deliver `Delete` for deleted posts
- [ ] Deliver `Follow` when following remote user
- [ ] Deliver `Undo Follow` when unfollowing
- [ ] Deliver `Like` for favourites
- [ ] Deliver `Announce` for boosts
- [ ] Deliver `Accept`/`Reject` for follow responses

### Task 5.16: Remote Actor Resolution
- [ ] Create `internal/federation/resolver.go`
- [ ] Implement WebFinger client
- [ ] Fetch remote actor profiles
- [ ] Cache actors in database
- [ ] Refresh stale actor data (>24h old)

### Task 5.17: Remote Content Fetching
- [ ] Fetch remote statuses when needed
- [ ] Fetch thread context
- [ ] Handle media attachments

---

## Milestone 6: Background Workers

**Goal:** Async job processing for reliability

### Task 6.1: Choose Job Queue
- [ ] Evaluate River (PostgreSQL)
- [ ] Evaluate Asynq (Redis)
- [ ] Make decision and document reasoning

### Task 6.2: Worker Infrastructure
- [ ] Install chosen library
- [ ] Create `internal/workers/worker.go`
- [ ] Add worker startup to `serve.go`
- [ ] Create job registration system

### Task 6.3: Delivery Jobs
- [ ] Create `DeliverActivityJob`
  - [ ] Accept: activity, recipient inbox
  - [ ] Sign and POST activity
  - [ ] Handle retries

### Task 6.4: Remote Processing Jobs
- [ ] Create `ProcessRemoteAccountJob`
  - [ ] Fetch and store remote actor
- [ ] Create `FetchRemoteStatusJob`

### Task 6.5: Scheduled Status Job
- [ ] Create `ScheduledStatusJob`
  - [ ] Poll scheduled_statuses table
  - [ ] Publish when time is reached

### Task 6.6: Media Processing Jobs
- [ ] Create `MediaProcessingJob`
  - [ ] Generate thumbnails
  - [ ] Extract metadata
  - [ ] Calculate blurhash

### Task 6.7: Link Crawler Job
- [ ] Create `LinkCrawlerJob`
  - [ ] Fetch URL metadata
  - [ ] Generate preview cards

### Task 6.8: Cleanup Jobs
- [ ] Create `StatusCleanupJob`
  - [ ] Honor account cleanup policies
- [ ] Create `MediaCleanupJob`
  - [ ] Remove orphaned media

---

## Milestone 7: Verification & Testing

**Goal:** Ensure everything works correctly

### Task 7.1: Unit Tests
- [ ] All existing tests pass: `go test ./...`
- [ ] Add tests for new handlers
- [ ] Add tests for federation code
- [ ] Code coverage > 60%

### Task 7.2: Integration Testing
- [ ] Set up test Mastodon instance (Docker)
- [ ] Test follow from Mastodon to Gotodon
- [ ] Test follow from Gotodon to Mastodon
- [ ] Test posting and visibility
- [ ] Test replies across servers
- [ ] Test boosts across servers
- [ ] Test favourites across servers

### Task 7.3: API Compatibility Testing
- [ ] Test with Mastodon.py client
- [ ] Test with official Mastodon web UI
- [ ] Test with mobile apps (Tusky, Ice Cubes, etc.)

### Task 7.4: Performance Testing
- [ ] Load test federation delivery
- [ ] Verify streaming under load
- [ ] Check database query performance

---

## Progress Summary

| Milestone | Status | Progress |
|-----------|--------|----------|
| 1. Schema | Not Started | 0% |
| 2. v1 API | Not Started | 0% |
| 3. v2 API | Not Started | 0% |
| 4. Admin API | Not Started | 0% |
| 5. Federation | Not Started | 0% |
| 6. Workers | Not Started | 0% |
| 7. Testing | Not Started | 0% |

---

## Notes

- Mark tasks with `[x]` when complete
- Add notes under tasks for decisions or blockers
- Update progress summary after completing milestones
