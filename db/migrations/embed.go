// Package migrations provides embedded SQL migrations for goose.
package migrations

import "embed"

//go:embed *.sql
var Migrations embed.FS
