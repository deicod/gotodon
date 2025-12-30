# Gotodon <-> Mastodon 4.5.3 Compatibility Plan

Last updated: 2025-12-29
Target: Mastodon 4.5.3
Scope: Server-side compatibility (schema, API v1/v2/admin, federation, streaming, OAuth/OIDC, background jobs/services, tests).
Out of scope for now: Web UI parity and /api/web endpoints.
Assumptions: No database provisioned yet, so new migrations can be authored but do not need to be applied.

## Evidence sources
- Mastodon routes: `mastodon/config/routes/api.rb`
- Schema diff: `SCHEMA_DIFF.md`
- Gotodon routes: `cmd/serve.go` and `internal/api/**`
- Implementation notes: inline comments and mock fallbacks in handlers

## Current parity snapshot (by subsystem)
- Schema: High. Tables and columns align; one extra table remains (`preferences`) and many index name/shape mismatches remain in `SCHEMA_DIFF.md`.
- API v1: Medium. Core handlers exist, but several endpoints are missing and some responses are static or minimal.
- API v2: Medium-low. Core endpoints exist, but filters/notifications/admin coverage is incomplete and several responses are static or mock.
- Admin API: Medium-low. Core CRUD exists, but report workflow, email domain blocks, instance config, metrics, roles, and audit logging are incomplete.
- OAuth/OIDC: Low. Authorization code flow is stubbed; password grant uses plaintext compare; userinfo and discovery endpoints are missing.
- ActivityPub/Federation: Low-medium. Inbox handlers exist, but outbox/collections/pages and alias routes are missing or empty.
- Streaming: Medium. SSE/WS hub exists; wildcard and multistream support are in place; event coverage remains incomplete.
- Jobs/Services: Medium-low. Delivery, media processing, link crawling, and cleanup exist; many Mastodon background features are missing.
- Tests/Compatibility: Partial. Unit tests exist, but integration and compatibility suites are missing.

---

## Schema & Data Model
Status: High (parity with remaining gaps).

Gaps
- Extra `preferences` table (Mastodon uses `users.settings` / `web_settings`).
- Index name/shape mismatches listed in `SCHEMA_DIFF.md`.
- Conversation unread tracking is stubbed (no persistent read state).

Plan
1) Add migration `040_migrate_preferences.sql` to move preferences into `users.settings` and `web_settings`, then drop `preferences`.
2) Reconcile remaining index differences from `SCHEMA_DIFF.md` (name alignment + unique constraints).
3) Add read/unread tracking for conversations (align with Mastodon logic; likely `conversation_mutes` or new table).
4) Regenerate sqlc (`sqlc generate`) and update query usage after each schema change.
5) Add schema parity tests (create tables + verify index names/uniqueness).

---

## API v1
Status: Medium.

Missing routes (from `mastodon/config/routes/api.rb`)
- GET /api/oembed
- GET /api/v1_alpha/async_refreshes/:id
- [x] GET /api/v1/statuses (index)
- [x] POST /api/v1/statuses/:id/mute
- [x] POST /api/v1/statuses/:id/unmute
- [x] PUT /api/v1/statuses/:id/interaction_policy
- GET /api/v1/timelines/link
- GET /api/v1/streaming/(*any)
- [x] POST /api/v1/conversations/:id/unread
- [x] DELETE /api/v1/media/:id
- /api/v1/filters (all CRUD)
- GET /api/v1/notifications/requests/:id
- POST /api/v1/notifications/requests/accept (bulk)
- POST /api/v1/notifications/requests/dismiss (bulk)
- GET /api/v1/notifications/requests/merged
- [x] GET /api/v1/domain_blocks/preview
- GET /api/v1/instance/terms_of_service/:date
- GET /api/v1/accounts (index)
- GET /api/v1/accounts/:id/lists
- GET /api/v1/accounts/:id/identity_proofs
- GET /api/v1/accounts/:id/featured_tags
- GET /api/v1/accounts/:id/endorsements
- POST /api/v1/accounts/:id/endorse
- POST /api/v1/accounts/:id/unendorse
- GET /api/v1/featured_tags/suggestions
- [x] GET /api/v1/trends (alias for tags index)

Behavior gaps
- /api/v1/instance uses static data and mismatched version string.
- Trends links now backed by preview cards; suggestions deletion and several account/status payloads are still minimal or mock.
- Scheduled status creation is missing (no `scheduled_at` in POST /api/v1/statuses).

Plan
1) Implement all missing routes listed above and wire queries/services.
2) Align v1 payloads to Mastodon formats (accounts, statuses, notifications, trends, search results, instance info).
3) Add scheduled status creation in `POST /api/v1/statuses` with worker integration.
4) Implement v1 filters API (legacy semantics) and migration paths from v2 filters if needed.
5) Add tests for each new/changed handler (including error cases and auth boundaries).

---

## API v2
Status: Medium-low.

Missing routes (from `mastodon/config/routes/api.rb`)
- [x] POST /api/v2/notifications/clear
- [x] GET /api/v2/filters/:filter_id/keywords
- [x] GET /api/v2/filters/keywords/:id
- [x] PUT /api/v2/filters/keywords/:id
- [x] GET /api/v2/filters/:filter_id/statuses
- [x] POST /api/v2/filters/:filter_id/statuses
- [x] GET /api/v2/filters/statuses/:id
- [x] DELETE /api/v2/filters/statuses/:id
- [x] GET /api/v2/admin/accounts

Behavior gaps
- /api/v2/instance returns static placeholder data.
- /api/v2/suggestions returns mock data.
- /api/v2/search responses are minimal vs Mastodon payloads.

Plan
1) Implement full v2 filters keyword/status endpoints and align to custom_filters schema.
2) [x] Add /api/v2/notifications/clear and /api/v2/admin/accounts.
3) Align v2 instance, suggestions, and search payloads to Mastodon formats.
4) Add tests for each new/changed v2 endpoint.

---

## Admin API (v1 + v2)
Status: Medium-low.

Missing routes or mismatches
- /api/v1/admin/email_domain_blocks (all CRUD).
- Report workflow actions: assign_to_self, unassign, reopen, and update (PUT /api/v1/admin/reports/:id).
- Admin trends publishers: /api/v1/admin/trends/links/publishers (preview_card_providers) + approve/reject.
- Path mismatch: Mastodon uses /unsensitive; Gotodon currently registers /unsensitize.
- [x] /api/v2/admin/accounts (index).

Behavior gaps
- Admin instance config is in-memory and not persisted.
- Dimensions/measures/retention endpoints return empty arrays.
- Admin action logs and role handling are stubbed.
- Custom emoji categories are missing in responses.

Plan
1) Implement email domain block endpoints (use `email_domain_blocks` table).
2) Add full report workflow actions and update endpoint.
3) Add preview_card_providers moderation endpoints.
4) Align admin account actions and path names (unsensitive) and wire audit logging.
5) Implement role handling via `user_roles` and include role in admin responses.
6) Persist admin instance config (backed by settings tables) and enforce auth.
7) Implement real metrics for dimensions/measures/retention.
8) Add custom emoji categories in admin responses.
9) [x] Add v2 admin accounts endpoint and tests.

---

## OAuth/OIDC
Status: Low.

Gaps
- Authorization code flow is stubbed.
- Password grant uses plaintext compare.
- Missing /oauth/userinfo and /.well-known/oauth-authorization-server.
- Token refresh/expiry and revocation semantics are incomplete.

Plan
1) Choose OAuth/OIDC server implementation (use ory/fosite if appropriate).
2) Implement full authorization code + PKCE flow with sessions and persisted grants.
3) Implement bcrypt password verification and proper token lifecycle.
4) Add /oauth/userinfo and discovery metadata.
5) Add tests for OAuth flows (grant types, PKCE, refresh, revoke).

---

## ActivityPub / Federation
Status: Low-medium.

Gaps
- Outbox returns empty collections; no paging or items.
- Missing /ap alias routes.
- Missing collection endpoints (featured, tags), quote authorizations, followers synchronization.
- Missing /contexts endpoints and instance actor (/actor).
- Followers/following pages lack items.

Plan
1) Implement outbox collections and paging with serialized activities.
2) Add /ap/ alias routes matching Mastodon.
3) Add collections endpoints (featured, tags) and quote authorization endpoints.
4) Implement /contexts endpoints and instance actor.
5) Return followers/following OrderedCollectionPages with items.
6) Add federation tests for inbox + newly implemented endpoints.

---

## Streaming
Status: Low-medium.

Gaps
- Missing /api/v1/streaming/(*any) wildcard route.
- Missing multistream support (stream[] and multiple streams per connection).
- Event coverage is incomplete vs Mastodon (filters_changed, conversation, announcements, status.update, etc).

Plan
1) [x] Add wildcard streaming route and per-stream path handling.
2) [x] Support multi-stream subscriptions (stream[] + tag/list combos).
3) [ ] Align event types and payload formats to Mastodon.
4) [x] Add streaming tests (SSE + WS) and multi-stream coverage.

---

## Jobs & Services
Status: Medium-low.

Gaps
- Scheduled status creation is missing; workers exist but no creation flow in v1.
- Many Mastodon background features are missing (backups, bulk imports, relays, webhooks, follow recommendations, annual reports generation, etc).

Plan
1) Finish scheduled status creation + enqueue/publish flow.
2) Implement missing background jobs/services per Mastodon (backups, imports, relays, webhooks, follow recommendations).
3) Add tests for workers and job retries.

---

## Tests & Compatibility Verification
Status: Partial.

Plan
1) Add handler tests for every new endpoint and behavior change.
2) Add schema parity and query-level tests for new migrations.
3) Add integration tests for key flows (follow, post, reply, boost, favourite, media upload).
4) Add client compatibility tests (Mastodon.py, Tusky, Ice Cubes).
5) Add federation compatibility tests against a Mastodon 4.5.3 instance.

---

## Deferred (explicitly out of scope for now)
- Web UI parity and /api/web endpoints.
