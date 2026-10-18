# Agent guidelines

## This is a brand-new app: no legacy code, ever

PaperGo is being built from scratch. It has never been released and has no deployments, users, clients or stored data to preserve.

- **No legacy or compatibility code.** Do not keep old behaviour alongside new behaviour. That means no deprecated fields, routes or parameters, no "legacy" modes, shims, adapters, fallbacks or dual code paths.
- **When a feature ships, delete what it replaces.** Change the code, its callers, tests, OpenAPI contract and docs in the same change. Never leave the previous version in place for compatibility.
- **No development migrations.** The whole schema is one file, `migrations/20261007000100_schema.sql`. Edit it in place, keep `ent/schema` in sync (`go generate ./ent`), then run `atlas migrate hash --dir file://migrations`. Never add another development migration file. ISSUE-23 release CI alone generates cumulative published upgrade SQL, commits it on the `release-migrations` artifact branch and embeds it in release builds.
- **No development backfills or upgrade paths.** No development data needs to survive a schema change. Release-engine fixtures test ISSUE-23 sequential upgrades and rollback; they are not development upgrade paths. Local databases are disposable: delete `data/` and run `atlas migrate apply --env local`.
- **Docs describe the current app only.** Do not document past behaviour, conversions or "previously" notes.
