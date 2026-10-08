# Agent guidelines

## This is a brand-new app: no legacy code, ever

PaperGo is being built from scratch. It has never been released and has no deployments, users, clients or stored data to preserve.

- **No legacy or compatibility code.** Do not keep old behaviour alongside new behaviour. That means no deprecated fields, routes or parameters, no "legacy" modes, shims, adapters, fallbacks or dual code paths.
- **When a feature ships, delete what it replaces.** Change the code, its callers, tests, OpenAPI contract and docs in the same change. Never leave the previous version in place for compatibility.
- **No migrations.** The whole schema is one file, `migrations/20261007000100_schema.sql`. Edit it in place, keep `ent/schema` in sync (`go generate ./ent`), then run `atlas migrate hash --dir file://migrations`. Never add another migration file.
- **No backfills, upgrade paths or upgrade tests.** No data needs to survive a schema change. Local databases are disposable: delete `data/` and run `atlas migrate apply --env local`.
- **Docs describe the current app only.** Do not document past behaviour, conversions or "previously" notes.
