---
description: Development workflow for Gotodon
---

# Gotodon Development Workflow

## Prerequisites
- Go 1.25+
- PostgreSQL 18+
- Redis 7+
- sqlc (`go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest`)
- golangci-lint (`go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest`)

## Standard Commands

### Build
// turbo
```bash
go build ./...
```

### Test
// turbo
```bash
go test ./... -v
```

### Lint
// turbo
```bash
golangci-lint run ./...
```

### Generate sqlc queries
```bash
sqlc generate
```

### Run server
```bash
go run . serve --port 3000
```

## Before Committing
Run all checks:
// turbo-all
```bash
go build ./...
go test ./... -v
golangci-lint run ./...
```

## Database Migrations
Migrations are in `db/migrations/`. Apply them manually:
```bash
psql -d gotodon_development -f db/migrations/001_initial_schema.sql
```

## Project Structure
- `cmd/` - CLI commands
- `internal/` - Private application code
  - `api/` - HTTP handlers
  - `config/` - Configuration
  - `db/` - Database layer (sqlc generated)
  - `services/` - Business logic
  - `serializers/` - JSON response formatting
- `pkg/` - Public packages
- `db/migrations/` - SQL migrations
- `db/queries/` - sqlc query definitions