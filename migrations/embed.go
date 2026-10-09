// Package migrations embeds the SQL migration files for golang-migrate.
package migrations

import "embed"

// FS contains every up and down migration in this directory.
//
//go:embed *.sql
var FS embed.FS
