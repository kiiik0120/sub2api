# Kox database extension

Kox business migrations are deliberately separate from upstream Sub2API
`backend/migrations`. Run this one-shot job only after the official Sub2API
migrations have completed:

```sh
./extensions/kox/apply-migrations.sh
```

It records versions in `kox_extension_schema_migrations`; upstream releases do
not read, overwrite, or reorder this ledger.
