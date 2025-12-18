# Repository Guidelines

## Project Structure & Module Organization
- `main.go` boots the Cobra CLI; commands live in `cmd/` (`serve`, `migrate up/down/status/reset`) and drive server startup and migrations.
- Application logic sits in `internal/` (API handlers in `internal/api/v1` and `v2`, plus auth, config, media, streaming, services, workers, and ActivityPub helpers). Shared utilities are in `pkg/` (activitypub, oauth, snowflake).
- Database SQL lives in `db/migrations` (goose-numbered files) and `db/queries` (sqlc inputs) that generate the typed client in `internal/db`.
- `mastodon/` contains the upstream Mastodon reference/frontend; change it only when syncing UI assets. Runtime uploads default to `./uploads`.

## Implementation Quality
**Critical: No MVP or deferred implementations.** When implementing a feature:
- **Complete the full implementation** - no TODO comments, no stub functions, no "defer to later" patterns
- **Every feature must be production-ready** - if you can't do it properly, don't start it
- **No partial implementations** - either a feature works completely or it shouldn't exist
- **Tests are mandatory** - every handler needs corresponding test coverage
- **Handle all edge cases** - error states, empty inputs, invalid data, auth boundaries

This applies to all code: API handlers, database queries, tests, and documentation.

## Build, Test, and Development Commands
- `go build ./...` (or `go run . serve --config ./gotodon.yaml --port 3000`) builds/runs the API; requires PostgreSQL and Redis reachable via `GOTODON_*` or `DB_*`/`REDIS_*` env vars.
- `go run . migrate up` / `... down|status|reset` runs goose migrations from `db/migrations`.
- `sqlc generate` regenerates the typed DB client in `internal/db` from `db/queries`.
- `go test ./...` exercises unit tests; some admin/federation cases are skipped without backing DB/Redis. Add focused runs with `go test ./internal/api/v1 -run TestStatusesHandler`.

## Coding Style & Naming Conventions
- Target Go 1.25; format with `gofmt` (or `goimports`) before submitting; tab indentation.
- Keep packages cohesive; prefer small interfaces and table-driven tests. Follow Cobra `Use`/`Short` patterns for CLI flags.
- Migrations use zero-padded numeric prefixes (`001_initial_schema.sql`); keep names descriptive and additive.

## Testing Guidelines
- Place tests beside code as `_test.go`; favor table-driven cases and HTTP handler tests using `httptest`.
- When tests need Postgres/Redis, set `DB_*`/`REDIS_*` env vars or skip with `-short`; ensure new queries include coverage in `internal/db` and higher-level handlers.

## Commit & Pull Request Guidelines
- Commit history follows conventional commits (`feat:`, `docs:`, `fix:`). Use imperative scope-aware messages.
- PRs should list the change summary, config/env/migration impacts, and tests run (`go test ./...`, migration commands). Link related issues; add screenshots or sample requests when altering API responses or UI assets under `mastodon/`.

