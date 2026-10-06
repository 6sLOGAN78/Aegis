package migrations

import "embed"

// FS embeds all SQL migration files for goose.
//
//go:embed *.sql
var FS embed.FS
