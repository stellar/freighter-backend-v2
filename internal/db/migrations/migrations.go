// Package migrations embeds the SQL migration files applied by sql-migrate.
package migrations

import "embed"

// FS holds the embedded .sql migration files, applied in lexical order.
//
//go:embed *.sql
var FS embed.FS
