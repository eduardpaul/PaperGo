# Agent guidelines

## This is a brand-new app: no legacy code, ever

PaperGo is being built from scratch. It has never been released and has no deployments, users, clients or stored data to preserve.

- **No legacy or compatibility code.** Do not keep old behaviour alongside new behaviour. That means no deprecated fields, routes or parameters, no "legacy" modes, shims, adapters, fallbacks or dual code paths.
- **When a feature ships, delete what it replaces.** Change the code, its callers, tests, OpenAPI contract and docs in the same change. Never leave the previous version in place for compatibility.
- **Schema masters live in DAL.** `internal/dal/schema/sqlite.sql` and `internal/dal/schema/postgres.sql` are the sources of truth. Edit them directly, keep the two engines consistent, and regenerate Bob models when their shape changes. Do not derive schema changes from `migrations/`, or add development migration files.
- **No development backfills or upgrade paths.** No development data needs to survive a schema change. Local databases are disposable; recreate them from the selected DAL schema. Release upgrade fixtures are not development upgrade paths.
- **Docs describe the current app only.** Do not document past behaviour, conversions or "previously" notes.

## Database access

- Keep SQL and Bob query construction inside `internal/dal`. Application packages call typed repository methods for database reads and writes.
- Never spell a fixed value set (a column with `CHECK (column IN (...))`) as string literals in Go. Use the domain package's named type and constants; see "Fixed value sets" in `internal/dal/README.md`.
