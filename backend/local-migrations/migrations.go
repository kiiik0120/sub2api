// Package localmigrations contains fork-specific database migrations.
//
// Keep custom schema changes here instead of migrations/ so upstream syncs do
// not contend for migration numbers or alter the upstream migration history.
package localmigrations

import "embed"

// FS contains local SQL migrations that are applied after upstream migrations.
//
//go:embed *.sql
var FS embed.FS
