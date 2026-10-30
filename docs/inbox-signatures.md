# Inbox signatures: freshness and replay protection

Every `POST` to an inbox (`/inbox`, `/users/{username}/inbox`, `/actor/inbox` and
their `/ap/` aliases) passes two middlewares before the activity is processed:

1. `middleware.VerifyHTTPSignature` checks the HTTP signature, including its age.
2. `middleware.RejectInboxReplays` makes sure each signed request is processed at
   most once while its signature can still verify.

## Signature freshness

The signature must cover `(request-target)`, `host`, `digest`, and either `date` or
`(created)`. `(created)` and `(expires)` are only accepted with `hs2019`. The signed
creation time is `(created)` when it is signed and the `Date` header otherwise. The
limits match Mastodon's `SignedRequest`, so deliveries from Mastodon and other
ActivityPub servers keep verifying:

| Limit | Value |
|---|---|
| Validity without a signed `(expires)` | 5 minutes after creation |
| Maximum validity with a signed `(expires)` | 12 hours after creation (later `(expires)` values are capped) |
| Clock skew tolerated | 1 hour, both on the creation time and on expiry |

A signature is rejected with `401 Unauthorized` when:

- its creation time is more than 1 hour in the future;
- the current time is more than 1 hour past its expiry.

For a typical `Date`-signed delivery, the signature verifies from creation − 1 hour
until creation + 1 hour 5 minutes. `RequestSignature.ValidUntil` reports the last
instant a parsed signature still verifies.

## Replay protection

Within its validity window, a captured request would verify again if it were
resent. Without protection, a replayed `Follow` sent after its `Undo` would
re-establish the follow, a replayed `Like` would favourite and notify again, and a
replayed `Update` without `updated` would record another edit.

`RejectInboxReplays` identifies a signed request by three values, all bound by the
signature: the verified actor, the request path, and the SHA-256 of the body. A
legitimate redelivery is signed again, but it carries the same body to the same
inbox, so it has the same identity. Each request is handled according to its state:

| State of the request | Response | Processed? |
|---|---|---|
| Not seen before | the inbox handler's response | yes |
| Already processed (handler answered 2xx) | `202 Accepted` | no |
| Being processed right now | `503 Service Unavailable`, `Retry-After: 60` | no; the sender retries |
| Previous attempt failed (non-2xx or panic) | the inbox handler's response | yes, the failure was not recorded |

These rules keep sender retries safe. A retry after a failure is processed. A
retry after a success whose response was lost is acknowledged without being
processed again. A concurrent duplicate is deferred until the first attempt
finishes.

### Retention

A processed request is kept until its signature stops verifying (`ValidUntil`),
plus a 5-minute margin for clock drift between instances. Each later delivery with
the same identity extends this to its own signature's lifetime, and retention is
never shortened. A replay therefore cannot outlive its record: once the record
expires, every captured copy of the request fails the freshness check.

A claim on a request that is being processed lasts 5 minutes. If the processing
server dies mid-request, deliveries are unblocked after that time.

### Storage

| Deployment | Store | Notes |
|---|---|---|
| Redis reachable at startup | `RedisInboxReplayStore` | Shared by all instances. Keys are `gotodon:inbox_replay:<sha256>`, values are `claim:<token>` or `done`, and expiry is set with `PX`. |
| No Redis | `MemoryInboxReplayStore` | Per process; the server logs a warning at startup. It tracks up to 524,288 requests and refuses new ones with `503` when full rather than forgetting live entries. |

Running more than one instance behind the same inboxes requires Redis. Otherwise a
replay sent to a different instance is not recognized. If the store fails, the
server fails closed: the delivery is answered `503` with `Retry-After` and is not
processed.

## Handler idempotency

After the window, only the actor's own server can produce a valid signature.
Redeliveries after that point are legitimate, and the activity handlers in
`internal/federation` process them without duplicating side effects:

| Activity | Behaviour on repeated delivery |
|---|---|
| `Follow` | An existing follow is kept and `Accept` is sent again; follow requests are unique per pair. |
| `Accept` / `Reject` | No-op once the follow request has been resolved. |
| `Undo` | Removes only state that still exists; counters are decremented only when a favourite or reblog was removed. |
| `Create` | No-op when the note URI is known. Deleted notes keep their URI, so they are not recreated. |
| `Announce` | No-op when the activity URI or a reblog of the same status by the actor exists. Undone reblogs keep their URI and are not restored. |
| `Like` | No-op when the favourite exists (unique per account and status). |
| `Block` | No-op when the block exists (unique per pair). |
| `Delete` | No-op once the note is deleted or the actor is suspended. |
| `Update` (note) | Ignored when `updated` is not newer than the current edit. An `Update` without `updated` is applied on every delivery and relies on replay protection inside the window. |
| `Update` (actor) | Overwrites profile fields with the same values. |

## Library use

`activitypub.InboxHandler` in `pkg/activitypub` enforces signature coverage,
freshness and key ownership, but it does not track replays. Callers that process
state-changing activities with it should keep processed requests until
`RequestSignature.ValidUntil`, as `RejectInboxReplays` does.
