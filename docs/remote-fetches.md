# Remote fetches: destination addresses, redirects and response limits

Federation makes gotodon request URLs that other servers choose: an inbox
delivery names its actor, actor documents name their inboxes, and activities
reference objects and collections. A URL like that could point at the server's
own network, for example the cloud metadata endpoint at `169.254.169.254`, a
database on `10.0.0.5` or an admin interface on `localhost`. Every request to
such a URL therefore goes through a `pkg/safehttp` client. That client connects
only to public addresses, holds every redirect to the same rules and caps what
it reads.

## Requests covered

| Request | URL comes from | Code |
|---|---|---|
| Actor fetch to verify an inbox signature or a followers synchronization request | the `actor` of the unauthenticated incoming request | `internal/api/activitypub` (`actorFetcher`) |
| Remote account resolution (search, follows, mentions, refreshes) | an actor URI, or the WebFinger answer for an `acct` | `internal/federation` (`RemoteActorResolver`) |
| WebFinger lookups | the domain of an `acct` | `pkg/activitypub` (`WebFingerClient`) |
| Objects and collections (boosts, remote statuses, threads and replies) | URIs in remote activities and documents | `internal/federation` (`httpObjectFetcher`) |
| Signed activity deliveries | `inbox` and `sharedInbox` of remote actor documents | `pkg/activitypub` (`SignedClient`) |

## Destination addresses

Only globally reachable unicast addresses are contacted. These are refused:

| Family | Refused blocks |
|---|---|
| IPv4 | `0.0.0.0/8`, `10.0.0.0/8`, `100.64.0.0/10`, `127.0.0.0/8`, `169.254.0.0/16`, `172.16.0.0/12`, `192.0.0.0/24`, `192.0.2.0/24`, `192.88.99.0/24`, `192.168.0.0/16`, `198.18.0.0/15`, `198.51.100.0/24`, `203.0.113.0/24`, `224.0.0.0/4`, `240.0.0.0/4` |
| IPv6 | everything outside `2000::/3` (loopback, unspecified, IPv4-compatible, unique local, link-local, site-local, multicast, ...), plus `2001::/23`, `2001:db8::/32` and `3fff::/20` |
| IPv6 carrying IPv4 | IPv4-mapped (`::ffff:a.b.c.d`), NAT64 (`64:ff9b::/96`) and 6to4 (`2002::/16`) addresses are judged by the IPv4 address they carry; local-use NAT64 (`64:ff9b:1::/48`) is refused |

The client enforces this when it connects, not only when it validates the URL:

1. A host name is resolved once per connection. If any address in the answer is
   refused, the whole answer is refused. A name that mixes public and internal
   addresses gets no connection, even over its public address.
2. The client dials the validated IP literals themselves, so nothing is looked
   up again between the check and the connect. A name server that answers with
   a public address first and an internal one later (DNS rebinding) cannot
   redirect the connection.
3. Each socket's address is checked once more just before it connects.
4. `HTTP_PROXY` and the other proxy environment variables are ignored. Through
   a proxy, the client would dial the proxy, and the destination could no
   longer be checked.

For hosts with both IPv4 and IPv6 addresses, the families are raced as
`net.Dialer` does. The first family in the answer gets 300 ms, and then the
other family is tried alongside it, so a broken IPv6 path does not stall the
request.

## URLs and redirects

| Rule | Value |
|---|---|
| Schemes | `http` and `https` |
| Credentials in the URL | refused |
| Port | 1–65535 |
| Redirects followed | 5 |

Each redirect target is checked like the original URL, so a redirect to an
internal address or another scheme fails the request. WebFinger domains must
be a bare `host[:port]`. An `acct` whose domain carries a path, query, fragment
or a second `@` is rejected before anything is requested.

## Responses

| Limit | Value |
|---|---|
| Time for a whole exchange, body included | 10 seconds; 30 seconds for deliveries |
| Response headers | 256 KiB |
| Actor, object and WebFinger documents | 1 MiB after decompression (`activitypub.MaxDocumentBytes`) |
| Status | `200` |
| Actor and object media type | `application/activity+json` or `application/ld+json` |
| WebFinger media type | `application/json` or any `application/*+json`, such as `application/jrd+json` |

A larger document fails the fetch: it is not truncated and parsed anyway. A
`Content-Length` above the limit fails the fetch before the body is read. A
compressed response is limited by its decompressed size.

## Failures

A refused or failed fetch is an ordinary fetch error. Callers see
`safehttp.ErrForbiddenAddress`, `safehttp.ErrUnsafeURL`,
`safehttp.ErrTooManyRedirects` or `safehttp.ErrBodyTooLarge` in the error
chain. For inbox deliveries, the actor fetch fails, so the request is answered
`401 Unauthorized` and the activity is not processed. A delivery whose inbox is
refused (`safehttp.IsRefused`) is not retried, because the policy would refuse
it again. The worker queue marks the job `asynq.SkipRetry`, and the in-process
queue drops it.

There is no allowlist for internal addresses. Instances on a private network or
on one machine cannot federate with each other directly, even for local testing.

## Outside this policy

These requests are not federation fetches and keep their own handling:

- Link previews and oEmbed (`internal/services`) check URLs with their own
  public-address rules.
- Web Push endpoints come from local users' subscriptions.
- Translation backends are configured by the administrator.

## Library use

`safehttp.NewClient(safehttp.Options{})` returns a client with the production
policy. All such clients share one connection pool. `Options.Lookup` and
`Options.Dial` replace name resolution and the connect step, but their answers
and dials still go through the address policy. `pkg/safehttp/safehttptest`
uses them to route public host names to `httptest` servers in tests.
`activitypub.FetchDocument` fetches and decodes one ActivityStreams document
with these limits.
