// Package migrations embeds the canonical SQL schema history (backend/migrations/*.sql).
// Files map 1:1 from Dolibarr install tables (llx_*) — see header comments per file.
package migrations

import "embed"

// FS holds all *.sql migration files.
//
//go:embed *.sql
var FS embed.FS
