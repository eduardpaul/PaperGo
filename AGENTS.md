# Agent guidelines

## Pre-release: no migrations, no compatibility layers

PaperGo has not been released and has no deployments or users. Until the first release:

- **Do not add new migration files.** Change the schema by editing the existing files in `migrations/` in place, then run `atlas migrate hash`. Local databases are disposable: delete `data/` and re-apply the migrations.
- **Do not write data backfills, upgrade paths, or upgrade tests.** No existing data needs to be preserved.
- **Do not add legacy or compatibility layers.** That includes deprecated fields or routes, dual code paths, shims, or fallbacks for old shapes. Change the code and its callers directly.

When the first release ships, replace this section with the real migration policy.
