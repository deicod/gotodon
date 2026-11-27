# Rate limits and client addresses

gotodon limits how often anonymous clients can try passwords and trigger
account mail. Without limits a client could guess passwords for any account,
or flood a mailbox with reset instructions. This page lists what gotodon
enforces itself and what is left to the reverse proxy. It also covers how
gotodon decides which address a request comes from, which the limits depend
on.

## Limits gotodon enforces

| Endpoint | Limit | Counted per |
|---|---|---|
| Password sign-in: `POST /oauth/authorize` | 25 per 5 min | client network |
| | 25 per hour | email address |
| Password reset request: `POST /auth/password` | 25 per 5 min | client network |
| | 5 per 30 min | email address |
| New password from a reset link: `PUT /auth/password`, or `POST /auth/password` with `_method=put` | shares the 25 per 5 min above | client network |
| Resend confirmation instructions: `POST /auth/confirmation` | 25 per 5 min | client network |
| | 5 per 30 min | email address |

The values are Mastodon's Rack::Attack limits for the same flows. They are
variables in `internal/api/middleware/ratelimit.go`; there is no
configuration for them.

- **Windows.** A window starts with the first attempt and lasts the period.
  Every attempt counts, successful or not.
- **Refusals.** An attempt beyond the limit gets `429 Too Many Requests`
  until the window ends. The response carries `X-RateLimit-Limit`,
  `X-RateLimit-Remaining`, `X-RateLimit-Reset` (as Mastodon sends them) and
  `Retry-After`. `/oauth/authorize` answers with Mastodon's JSON body
  `{"error":"Too many requests"}`. The `/auth/...` pages answer with an HTML
  page.
- **Order.** The network limit is checked first. An attempt it refuses is not
  counted against the email address, so one network cannot create counters for
  unlimited addresses.
- **Client network.** An IPv4 client is counted by its address. An IPv6
  client is counted by its /64, which a single subscriber usually holds whole.
- **Email addresses.** Addresses are compared without case and surrounding
  space, as accounts are looked up. `User@Example.org` and `user@example.org`
  share one limit.

### No account enumeration

Attempts are counted before any account is looked up, under the submitted
address, whether or not an account has it. A refused attempt never reaches the
database. An address without an account is therefore limited exactly like one
with an account, and the 429 response is identical. Below the limit the
endpoints answer as before: sign-in failures are `401 invalid_grant`, and the
reset and confirmation pages show the same neutral message for every address.

The per-address limit has a cost: anyone can use up an address's attempts, so
its owner may have to wait up to an hour to sign in. Mastodon makes the same
trade-off.

### Where counts are kept

With Redis, counts are kept in Redis under `gotodon:rate_limit:` and shared by
all instances using the same Redis database. Keys hold a SHA-256 hash of the
address or network, not the value itself, and expire with their window.
Windows are measured on Redis's clock, so instances with drifting clocks still
agree.

If Redis is unreachable at startup, gotodon logs a warning and keeps counts in
process memory. Each instance then limits on its own. The memory store tracks
at most 65,536 windows at once and drops ended ones.

An attempt that cannot be counted is refused with `503` and `Retry-After: 60`,
not let through. This happens when Redis returns an error, or when the memory
store is full of windows that have not ended.

## Client addresses and trusted proxies

Behind a reverse proxy, the peer address of every request is the proxy's. The
proxy reports the client's address in `X-Forwarded-For` or `X-Real-IP`. Clients
can send those headers too, so gotodon only believes them from trusted
proxies:

1. If the peer is not a trusted proxy, the peer address is the client address.
   Forwarding headers are ignored.
2. Otherwise gotodon reads `X-Forwarded-For` from right to left, skipping
   trusted proxies. The first address that is not a trusted proxy is the
   client. Addresses a client wrote into the header are further left and are
   never reached.
3. If the request has no `X-Forwarded-For`, a valid `X-Real-IP` from the
   trusted proxy is the client address.

The resulting address is the one rate limits count, and the one recorded in
login history, in suspicious sign-in mail and as a token's last-used IP. Code
reads it with `middleware.ClientIP`. Without the `ResolveClientIP` middleware
that `serve` installs, `ClientIP` returns the peer address and never trusts a
header.

### Configuring trusted proxies

Set `server.trusted_proxies`, or one of the environment variables
`GOTODON_SERVER_TRUSTED_PROXIES` and `TRUSTED_PROXY_IP` (Mastodon's name), to
IP addresses or CIDR ranges separated by commas or spaces:

```sh
TRUSTED_PROXY_IP="10.0.0.5, 2001:db8:10::/64"
```

When it is empty, gotodon trusts loopback, private and link-local networks.
These are the Rails defaults Mastodon uses: `127.0.0.0/8`, `::1`, `fc00::/7`,
`10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `169.254.0.0/16` and
`fe80::/10`. A configured list replaces the defaults. If you set it, include
every proxy between clients and gotodon, such as a local nginx (`127.0.0.1`)
behind a CDN. An invalid entry stops `serve` from starting.

Trusting a proxy is only safe if it does one of two things. It can append the
address it received the request from to `X-Forwarded-For`, as nginx does with
`proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;`. Or it can
replace the header with that address. Do not list a proxy, load balancer or
host that forwards client headers unchanged. Its clients could then choose
their own address and escape the limits.

With the defaults, any host on a private network that can reach gotodon
directly, bypassing the proxy, can also choose its address. Examples are other
containers on the same Docker network or machines on the same LAN. If that
matters, set the list to just your proxies.

If the proxy's address is not trusted, gotodon sees every request coming from
the proxy. All clients then share one set of limits, and 25 failed sign-ins
from anyone block password sign-in for everyone for five minutes. If you see
that, check `TRUSTED_PROXY_IP`.

## Limits left to the proxy

gotodon enforces only the limits listed above. It does not limit:

- the overall request rate per client or per access token. Mastodon allows 300
  requests per 5 minutes per anonymous client, and similar budgets per token
  and for media uploads.
- registrations (`POST /api/v1/accounts`), which need an application token.
- authenticated mail requests such as `POST /api/v1/emails/confirmations`.
- the number of concurrent connections. [HTTP server limits](http-server-limits.md)
  bound how long each one may take.

Public deployments should add a general request limit at the reverse proxy.
With nginx, for example:

```nginx
# In the http block: about 300 requests per 5 minutes per client address.
limit_req_zone $binary_remote_addr zone=gotodon:10m rate=1r/s;

# In the server block that proxies to gotodon.
limit_req zone=gotodon burst=300 nodelay;
limit_req_status 429;
```

A proxy limit keyed by `$binary_remote_addr` counts the address that connected
to the proxy. That is the real client only if the proxy is the first hop. A
CDN in front of nginx makes the CDN the first hop. Then configure nginx's
`real_ip` module with the CDN's ranges, or limit at the CDN.
