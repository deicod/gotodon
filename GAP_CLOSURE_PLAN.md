# Gotodon Port Status & Gap-Closure Plan

Last updated: 2026-06-09
Target: Mastodon 4.5.3 (server-side compatibility)
Supersedes: `PLAN.md` (last updated 2026-01-30; its subsystem ratings are stale)

## How this assessment was made

- Re-ran `go run ./tools/schema_diff` (2026-06-09) — see regenerated `SCHEMA_DIFF.md`.
- `ROUTE_DIFF.md` (2026-01-30): 273/273 `api.rb` routes covered, 0 missing, 0 extra; no
  routes have been removed since.
- Reviewed all 53 commits since `PLAN.md` was written (OAuth hardening, discovery parity,
  account validation, AP collections — most of PLAN.md's open items are actually done).
- Compared `mastodon/app/workers` (60 + 15 schedulers), `mastodon/app/services` (71),
  `mastodon/app/lib/activitypub/activity` (15 types), and `mastodon/config/routes/*.rb`
  against `internal/**` and `cmd/serve.go`.
- `go build ./...` passes; `go test ./...` passes for all 33 packages.

## Where the port stands (2026-06-09)

| Subsystem | Status | Notes |
|---|---|---|
| Schema | **Done** | 111/111 tables, indexes aligned. One cosmetic diff: `accounts.attribution_domains` is `text[]` vs Rails `varchar[]`. |
| API v1/v2 routes | **Done** | 273/273 from `api.rb`; payload alignment largely done through March. |
| Admin API | **High** | CRUD, report workflow, metrics, roles, audit logs in place. Account *actions* are shallow (see G5). |
| OAuth | **High** | PKCE, token reuse, scope enforcement, revoke semantics, userinfo, AS metadata; refresh/password grants disabled to match Doorkeeper config. PLAN.md's "Low" rating is obsolete. |
| Discovery / well-known | **High** | webfinger, host-meta (XML+JSON), nodeinfo, manifest, redirects, authorize_interaction, public web pages for accounts/statuses. |
| ActivityPub serving | **High** | Outbox with paging, featured/tags collections, contexts, instance actor, followers sync endpoint, quote authorizations, `/ap/` aliases, replies/likes/shares. |
| Federation (inbound) | **Medium** | 10/15 activity types handled. Missing: `Flag`, `Move`, `Add`, `Remove`, `QuoteRequest` (see G4). |
| Federation (outbound) | **Medium** | Status Create/Delete/Like/Announce/Undo/Update delivered via worker queue. Poll votes and reports do **not** federate (G4). No relays (G10). |
| Streaming | **High** | SSE/WS, wildcard route, multistream, full event set incl. `status.update`, `filters_changed`, `announcement.*`, `conversation`. |
| Search | **Done (by design)** | Postgres FTS (`websearch_to_tsquery`) instead of Elasticsearch. |
| Workers | **Medium** | 8 workers (delivery, remote account/status, scheduled status, media processing, link crawler, status/media cleanup) vs Mastodon's ~75. Most gaps map to missing features below. |
| **Email** | **Missing** | No SMTP/mailer at all (G1). |
| **Web Push delivery** | **Missing** | Subscriptions stored, VAPID configured, but pushes are never sent (G2). |
| **FASP** | **Missing** | Tables exist, zero endpoints/logic (G9). |
| Tests | **Good unit coverage** | All packages pass. No integration or client/federation compatibility suites (G13). |

The short version: **routes, schema, payloads, OAuth, discovery, and AP serving are in
good shape. The remaining work is the "engine room": email, push delivery, federation
side-effects (votes, reports, migration, quotes), account lifecycle depth, scheduled
maintenance jobs, and the 4.5-era FASP subsystem.**

---

## Gap-closure plan, task by task

Priorities: **P0** = blocks running a real instance · **P1** = federation correctness ·
**P2** = feature completeness · **P3** = ecosystem/verification.

### G1 · Mailer subsystem — P0

No email is ever sent. Registration (`POST /api/v1/accounts`) generates a confirmation
token that goes nowhere; `/api/v1/emails/confirmations` "resends" nothing; password
reset and security mails don't exist. Reference: `mastodon/app/mailers/*`.

- [x] G1.1 Add `internal/mailer` package: SMTP client (host/port/TLS/auth from config),
      async send via worker queue, plain-text templates with i18n-ready strings.
      Config keys mirroring Mastodon's `SMTP_*` env vars.
- [x] G1.2 Wire confirmation mail into registration and
      `POST /api/v1/emails/confirmations`; add `GET /auth/confirmation` (or equivalent
      link target) to consume the token and set `users.confirmed_at`.
- [x] G1.3 Password reset flow: request + token consumption endpoints
      (Mastodon: devise `recoverable`), mail template, token expiry.
- [x] G1.4 Security mails: new sign-in from unknown IP (optional, config-gated),
      password/email changed notices.
- [x] G1.5 Admin mails: new report notification, new pending account notification to
      moderators (`AdminMailer`).
- [x] G1.6 Tests: mailer unit tests with a fake SMTP sink; handler tests asserting
      enqueue.

### G2 · Web Push delivery — P0

`push_subscriptions` CRUD exists (v1 + `/api/web`), VAPID keys are in config, but no
notification is ever pushed. Reference: `mastodon/app/workers/web/push_notification_worker.rb`,
`Webpush` payload format in `app/serializers/web/notification_serializer.rb`.

- [ ] G2.1 Implement RFC 8291 (aes128gcm) message encryption + RFC 8292 VAPID signing
      in `internal/push` (or vendor a maintained Go webpush library).
- [ ] G2.2 Add `web_push_delivery` worker: on notification creation, match subscription
      `data.alerts` against notification type, build Mastodon-shaped payload
      (`access_token`, `preferred_locale`, `notification_id`, `notification_type`,
      `icon`, `title`, `body`), POST to endpoint.
- [ ] G2.3 Handle 404/410 responses by deleting dead subscriptions (Mastodon behavior).
- [ ] G2.4 Hook into the existing notification pipeline (everywhere `CreateNotification`
      is called: statuses, federation create/like/announce/update, follows, reports).
- [ ] G2.5 Tests: encryption round-trip vectors, payload shape, dead-subscription
      cleanup, alert-preference filtering.

### G3 · Account lifecycle (deletion / suspension side-effects) — P0

Admin suspend currently sets `suspended_at` + audit log only. Mastodon's
`SuspendAccountService` / `DeleteAccountService` / `UnsuspendAccountService` do much
more, and self-service deletion doesn't exist.

- [ ] G3.1 Implement `internal/services` account suspension: unmerge from timelines,
      reject pending follow requests, deliver `Update` (suspended actor) or `Delete`
      activity to followers' inboxes, strip from directory/search.
- [ ] G3.2 Implement account deletion service (used by admin and by federated `Delete`
      of local-account targets is N/A — admin + self): purge statuses (batched, with
      federated `Delete`), media, notifications, follows; keep tombstone per Mastodon.
- [ ] G3.3 Unsuspension service restoring visibility and re-delivering profile `Update`.
- [ ] G3.4 Wire into existing admin account action endpoint (`action=suspend`) and add
      side-effects behind the worker queue (long-running).
- [ ] G3.5 Tests for each service incl. federation deliveries enqueued.

### G4 · Federation completeness — P1

Inbound: 10/15 activity types are handled. Outbound: several interactions never leave
the instance. References: `mastodon/app/lib/activitypub/activity/*.rb`,
`app/services/activitypub/*`.

- [ ] G4.1 Inbound `Flag` → create remote-origin reports (maps to existing reports
      tables; actor = remote instance actor).
- [ ] G4.2 Inbound `Move` → account migration handling: update `moved_to_account_id`,
      enqueue refollow of target for local followers (Mastodon `MoveWorker`), create
      `account_migrations` record.
- [ ] G4.3 Inbound `Add`/`Remove` → remote pinned statuses (featured collection
      membership; `status_pins` for remote accounts).
- [ ] G4.4 Inbound `QuoteRequest` + outbound quote-approval flow (4.5 quotes):
      auto-accept per quote policy, deliver `Accept`/`Reject` with quote authorization
      stamp; revoke via `Delete` of authorization. Reference:
      `app/lib/activitypub/activity/quote_request.rb`, `verify_quote_service.rb`,
      `revoke_quote_service.rb`. (Serving `/quote_authorizations` already exists.)
- [ ] G4.5 Outbound poll votes: `POST /api/v1/polls/:id/votes` on a remote poll must
      deliver `Create{Note name=option}` to the poll origin (`vote_service.rb`).
      `NewPollsHandler` currently has no delivery dependency.
- [ ] G4.6 Outbound reports: `POST /api/v1/reports` with `forward=true` against remote
      accounts must deliver `Flag` to the remote instance (`report_service.rb`).
- [ ] G4.7 Outbound follower synchronization: attach `Collection-Synchronization`
      header on deliveries and react to inbound header by enqueuing a followers sync
      (`prepare_followers_synchronization_service.rb`). Serving endpoint exists.
- [ ] G4.8 Remote poll refresh on expiry / vote display (`fetch_remote_poll_service.rb`).
- [ ] G4.9 Federation tests per activity type (inbound fixtures + outbound enqueue
      assertions).

### G5 · Admin moderation depth — P1

- [ ] G5.1 Domain block side-effects: on severity=suspend, run suspension over all
      known accounts of that domain; on `reject_media`, purge media
      (`block_domain_service.rb`, `clear_domain_media_service.rb`). Generate
      `severed_relationships` + `relationship_severance_events` records and the
      corresponding `severed_relationships` notification.
- [ ] G5.2 Email domain block / canonical email block enforcement at registration time
      (verify; wire if missing).
- [ ] G5.3 Report action side-effects: `action=delete` statuses with federation,
      `mark_as_sensitive` with `Update` delivery (`approve_appeal_service.rb` scope
      excluded — appeals are settings-UI).

### G6 · Scheduled/maintenance jobs — P2

Gotodon has polling loops for scheduled statuses and status/media cleanup. Missing
schedulers that affect user-visible behavior (reference `app/workers/scheduler/*`):

- [ ] G6.1 Trends ingestion + decay: record tag/link/status usage into trend scores at
      status-create/interaction time and run periodic score recalculation + allowed
      filtering (Mastodon `Trends::*` with half-life decay). Today the read endpoints
      work but nothing populates scores, so trends stay empty on a live instance.
- [ ] G6.2 Scheduled announcements: publish/unpublish at `scheduled_at`/`ends_at`
      (`publish_scheduled_announcement_worker.rb`) + streaming events.
- [ ] G6.3 Poll expiration: notify local voters/owner when a poll closes
      (`poll_expiration_notify_worker.rb`), push `notification` events.
- [ ] G6.4 Annual report generation worker (read endpoints exist; nothing writes
      `generated_annual_reports`) — `generate_annual_report_worker.rb`.
- [ ] G6.5 User/maintenance cleanup: unconfirmed users (`user_cleanup_scheduler`),
      expired IP blocks (`ip_cleanup_scheduler`), suspended-user purge after grace
      period (`suspended_user_cleanup_scheduler`), auto-close registrations when mods
      inactive (`auto_close_registrations_scheduler`).
- [ ] G6.6 Follow recommendations refresh if/where query-time computation proves too
      slow (current query-time approach is acceptable; benchmark first).

### G7 · Backups & bulk imports — P2

Tables exist; no logic. These are settings-UI features in Mastodon but the artifacts
are served over HTTP and clients link to them.

- [ ] G7.1 Backup service: archive statuses (ActivityPub JSON), media, likes,
      bookmarks into a downloadable archive; `GET /backups/:id/download` route;
      `backup_worker` + mail when ready (depends on G1).
- [ ] G7.2 Bulk import service: following/blocking/muting/bookmarks/lists CSV imports
      with row workers and `bulk_imports` progress tracking.

### G8 · Webhooks — P2

`webhooks` table exists; no dispatch. Mastodon configures these via admin UI, but
dispatch is a server feature.

- [ ] G8.1 Webhook dispatch worker: on enabled events (`account.created`,
      `report.created`, `status.created`, etc.), POST signed payload
      (`X-Hub-Signature` HMAC-SHA256) with retries (`webhook_service.rb`,
      `webhooks/delivery_worker.rb`).
- [ ] G8.2 Minimal management surface (config file or admin endpoints) since the admin
      web UI is out of scope.

### G9 · FASP (Fediverse Auxiliary Service Providers) — P3

New in 4.4/4.5. Schema present, zero implementation. Reference:
`mastodon/config/routes/fasp.rb`, `app/controllers/api/fasp/**`, `app/workers/fasp/*`.

- [ ] G9.1 Provider registration endpoint `POST /api/fasp/registration` + key exchange.
- [ ] G9.2 Data-sharing v0: `backfill_requests` (+ continuation), `event_subscriptions`
      create/destroy; announcement workers for new/updated content to subscribed
      providers.
- [ ] G9.3 Debug callback endpoints (`/api/fasp/debug/v0/callback/responses`).
- [ ] G9.4 HTTP message signatures (RFC 9421) used by FASP, if not already covered by
      the existing httpsig middleware.

### G10 · Relays — P3

- [ ] G10.1 Relay subscribe/unsubscribe (Follow to relay actor, handle Accept),
      deliver public statuses to enabled relays, accept relayed Announces.
      (Admin UI for relays is out of scope; manage via config or minimal endpoints.)

### G11 · Web/auth surface decision — P3 (decide, then maybe do)

Out of scope per the original plan, but two items sit on the boundary because **API
clients depend on them**:

- [ ] G11.1 Decide: browser registration/login pages (devise equivalents: sign-up,
      confirmation landing, password reset landing). The OAuth authorize page already
      does email+password login; G1.2/G1.3 need at least minimal token-consumption
      pages. Keep these minimal HTML, no full web UI.
- [ ] G11.2 `GET /api/v1/invites/:invite_code` JSON + invite-aware registration
      (invites tables exist; verify handler depth).

### G12 · Schema cosmetics — P3

- [ ] G12.1 Align `accounts.attribution_domains` to `varchar[]` (or document the
      intentional divergence in `SCHEMA_DIFF.md` tooling as normalized-equal).

### G13 · Verification & compatibility — P1 (continuous)

- [ ] G13.1 Integration test harness: docker-compose Postgres+Redis, run migrations,
      exercise full flows (register→confirm→login→post→reply→boost→fav→media→follow).
- [ ] G13.2 Client compatibility pass: Mastodon.py scripted suite; manual Tusky /
      Ice Cubes / Elk smoke tests against a deployed instance.
- [ ] G13.3 Federation compatibility: two-way test against a real Mastodon 4.5.3
      container (follow, post, boost, like, poll vote, report forward, quote request,
      account move).
- [ ] G13.4 Regenerate `ROUTE_DIFF.md`/`SCHEMA_DIFF.md` in CI and fail on drift; add
      the route-diff generator to `tools/` (currently only `schema_diff` exists).
- [ ] G13.5 Keep this document current: check off tasks in the same PR that closes
      them; re-date the header.

---

## Suggested order of attack

1. **G1 mailer → G2 web push → G3 account lifecycle** (P0: an instance isn't honestly
   deployable without these).
2. **G4 federation completeness + G5 moderation depth** (P1: correctness toward other
   servers; G4.4 quotes and G4.2 move are the most user-visible 4.5 features).
3. **G13.1–13.3 verification harness** in parallel with the above — it pays for itself
   immediately.
4. **G6 schedulers → G7 backups/imports → G8 webhooks** (P2).
5. **G9 FASP, G10 relays, G11 decisions, G12 cosmetics** (P3).
