# Route Diff: Mastodon 4.5.3 api.rb vs Gotodon

Generated: 2026-01-30
Scope: Routes from `mastodon/config/routes/api.rb` compared against Go ServeMux registrations
in `internal/api/**` and `cmd/serve.go`.

Notes:
- Path params are normalized to `{param}` to avoid name-only differences.
- Rails `update` routes are treated as both `PATCH` and `PUT`.
- Go prefix handlers (paths ending with `/`) are treated as covering nested routes.
- This diff only reflects `api.rb` and does not include non-API routes (OAuth, ActivityPub,
  `.well-known`, NodeInfo) or other Mastodon route files.

## Summary
- Mastodon routes (api.rb): 273
- Gotodon routes: 273
- Missing in Gotodon: 0
- Extra in Gotodon: 0

## Missing in Gotodon

None.

## Extra in Gotodon

None.
