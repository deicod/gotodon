# Build and test checks

Every push to `main` and every pull request runs
`.github/workflows/ci.yml`. The workflow has two jobs. Either one failing
fails the check. This page covers what each job proves and how to run the
same commands locally.

Both jobs install the Go version named in `go.mod`, so CI moves with it.

## Unit job: build, vet and unit tests

```sh
go build ./...
go vet ./...
go test -short ./...
```

This needs no PostgreSQL or Redis. `-short` skips every test that needs one,
so a pass here says nothing about SQL queries, migrations or the Redis
stores.

## Integration job: every test, with PostgreSQL and Redis

The job starts PostgreSQL 18 and Redis 7 as service containers and runs the
whole suite against them:

```sh
GOTODON_TEST_REQUIRE_SERVICES=1 \
DATABASE_URL='postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable' \
REDIS_HOST=localhost REDIS_PORT=6379 \
go test -count=1 ./...
```

- **Database.** The database-backed tests read `DATABASE_URL`, or the
  `DB_*` / `GOTODON_DATABASE_*` variables when it is unset. Each test creates
  its own database or schema, applies every migration, and drops it at the
  end. The role therefore needs `CREATEDB`. Point it at a server you can
  spare, not production.
- **Redis.** The Redis-backed tests read `REDIS_HOST`, `REDIS_PORT` and
  `REDIS_PASSWORD`. Their keys use a prefix unique to the test, and each test
  deletes its keys when it ends.
- **No silent skips.** Without these settings the service-backed tests skip,
  so a plain `go test ./...` can pass without running them. With `GOTODON_TEST_REQUIRE_SERVICES` set to a
  true value (`1`, `true`), each of those tests fails instead, under
  `-short` too. A green integration job therefore means they all ran.
- **`-count=1`.** It stops `go test` from reusing cached results, which would
  not have run against the services.

To run it locally with Docker:

```sh
docker run -d --rm --name gotodon-test-pg -e POSTGRES_PASSWORD=postgres -p 5432:5432 postgres:18
docker run -d --rm --name gotodon-test-redis -p 6379:6379 redis:7
# then the go test command above
docker stop gotodon-test-pg gotodon-test-redis
```

## Writing a test that needs PostgreSQL or Redis

Skip through `testenv.SkipService` (in `internal/testenv`), never through
`t.Skip`. Do it under `-short` and when the settings above are missing.
`SkipService` skips the test, or fails it when
`GOTODON_TEST_REQUIRE_SERVICES` is set. Without it, the integration job
would pass with the test skipped.

## What the checks do not cover

Neither job runs a Mastodon client or federates with a real Mastodon server.
`GAP_CLOSURE_PLAN.md` tracks that work under G13.
