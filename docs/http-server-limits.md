# HTTP server limits: timeouts, header size and long-lived connections

gotodon serves the REST API, federation and streaming endpoints from one
`http.Server`, built by `web.NewServer` in `internal/web/server.go`. Without
limits a client could open many connections and then send its headers or body
a byte at a time, or never read the response. Each such connection would stay
open indefinitely. The server therefore applies these limits to every
connection:

| Limit | Value | Field |
|---|---|---|
| Request line and headers must arrive within | 10 s | `ReadHeaderTimeout` |
| Request line and headers may total at most | 64 KiB; larger requests get `431` | `MaxHeaderBytes` |
| The whole request, body included, must arrive within | 60 s | `ReadTimeout` |
| The response must be written within | 60 s after the headers were read | `WriteTimeout` |
| A keep-alive connection idle between requests is closed after | 120 s | `IdleTimeout` |

The values are constants next to `NewServer`; there is no configuration for
them. Ordinary API and federation requests are small and answered well within
these budgets.

## Requests with longer budgets

Some requests legitimately take longer. Their handlers replace the limits for
their own request only:

| Request | Budget | How |
|---|---|---|
| Media uploads: `POST /api/v1/media`, `POST /api/v2/media` | 15 min to receive the body and write the response | `web.ExtendUploadDeadlines` |
| Profile updates with avatar or header images: multipart `PATCH /api/v1/accounts/update_credentials` | 15 min to receive the body and write the response | `web.ExtendUploadDeadlines` |
| Server-Sent Events: `GET /api/v1/streaming/...` | open until the client disconnects; each write must finish within 10 s | `internal/streaming` (`SSEHandler`) |
| WebSocket: `GET /api/v1/streaming` with `Upgrade: websocket` | open until either side closes; each write must finish within 10 s and the client must answer pings within 60 s | `internal/streaming` (`WSHandler`) |

Uploads get the longer budget only after the request is authenticated, so
anonymous clients keep the ordinary limits. Fifteen minutes is enough for a
maximum-size video (about 99 MB) at about 1 Mbit/s. The size limits on upload
bodies still apply.

A Server-Sent Events stream sends a heartbeat comment every 15 s. A client that
stops reading is dropped once a write stalls for 10 s. A WebSocket upgrade
takes the connection over from the HTTP server, which clears the server's
deadlines. The WebSocket handler then pings every 30 s and closes connections
that send no pong within 60 s.

## Behind a reverse proxy

These limits apply between the proxy and gotodon. Keep the proxy's own limits
compatible with them:

- The read timeout for streaming locations must exceed the 15 s heartbeat.
  nginx's default `proxy_read_timeout` of 60 s is enough.
- The maximum request body must allow media uploads, for example
  `client_max_body_size 99m` in nginx.
- A proxy that buffers request bodies before forwarding them (nginx does by
  default) delivers uploads to gotodon quickly. Its own client timeouts then
  govern slow uploaders.
