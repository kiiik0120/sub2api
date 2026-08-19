#!/bin/sh
set -eu

# Run this as a one-shot job after the official Sub2API migrations have
# completed. It has an independent ledger so upstream migration numbering
# cannot collide with Kox business migrations.
: "${DATABASE_HOST:?DATABASE_HOST is required}"
: "${DATABASE_PORT:?DATABASE_PORT is required}"
: "${DATABASE_USER:?DATABASE_USER is required}"
: "${DATABASE_PASSWORD:?DATABASE_PASSWORD is required}"
: "${DATABASE_DBNAME:?DATABASE_DBNAME is required}"

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
export PGPASSWORD="$DATABASE_PASSWORD"
PSQL="psql -v ON_ERROR_STOP=1 -h $DATABASE_HOST -p $DATABASE_PORT -U $DATABASE_USER -d $DATABASE_DBNAME"

$PSQL <<'SQL'
CREATE TABLE IF NOT EXISTS kox_extension_schema_migrations (
    version TEXT PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
SQL

for migration in "$ROOT"/migrations/*.sql; do
    version=$(basename "$migration")
    applied=$($PSQL -tAc "SELECT 1 FROM kox_extension_schema_migrations WHERE version = '$version'")
    if [ "$applied" = "1" ]; then
        continue
    fi
    $PSQL -f "$migration"
    $PSQL -c "INSERT INTO kox_extension_schema_migrations(version) VALUES ('$version')"
done
