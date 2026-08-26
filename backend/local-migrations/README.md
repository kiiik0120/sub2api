# Fork-local migrations

Put fork-specific database changes in this directory, never in
`backend/migrations/`. They are embedded in the server binary and applied after
the upstream migration set. Use the `local_NNN_description.sql` naming pattern
so their entries in `schema_migrations` cannot collide with upstream files.
