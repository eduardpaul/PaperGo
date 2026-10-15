# Releases and database upgrades

Development keeps one editable schema: `migrations/20261007000100_schema.sql`.
Keep Ent in sync, regenerate its code and rehash Atlas as described in the README.
Do not create development migrations or preserve disposable development databases.
The source `migrations/release.json` is an empty build-input placeholder (`{}`),
not a history to maintain or commit. Tag CI replaces it in its temporary checkout.

## Publish a version

Merge the desired changes, then push an annotated tag:

```sh
git tag -a v0.1.0 -m 'PaperGo 0.1.0'
git push origin v0.1.0
```

Use `vX.Y.Z` for stable releases and `vX.Y.Z-rc.N` for release candidates.
The release workflow runs the existing CI and vulnerability workflows against
that tag, validates a forward-only published history, and prepares the database
bundle. It builds and runs native smoke tests for Linux amd64/arm64, macOS Apple Silicon, and Windows amd64, using [GitHub hosted runners](https://docs.github.com/en/actions/reference/runners/github-hosted-runners). Each archive includes `papergo`,
`papergo-admin`, the database bundle and this guide. Both executables support
`--version`; version, commit and commit date are injected into the build.
The Linux executables need glibc, and ONNX inference needs libstdc++. Archives include the cgo-linked PDFium and zvec libraries, ONNX Runtime and native license notices; WebP and tokenizers are statically linked. Linux and macOS locate libraries in `lib/` beside the executables; Windows places DLLs beside them. OCR additionally needs `ONNXRUNTIME_LIB` and `OCR_MODELS` ([OCR](ocr.md#configuration)). The Docker image bundles the OCR models and enables it by default. PDF text reading is always available.

Publication creates a draft, uploads all four archives plus
`release-database.json` and `SHA256SUMS`, then publishes it. Stable releases become
Latest; release candidates are marked as prereleases. No personal access token
is required: the publication job uses GitHub's repository token with
`contents: write`. Verify Actions is enabled and organization policy permits
that permission and the native hosted runners. Linux ARM runners require runner
availability for the repository's plan; a repository without that runner must
provide a native ARM runner with an appropriate label.

If a build fails, fix it and tag a new version. If a transient failure occurs,
rerun the failed workflow or use the release workflow's manual dispatch with the
same existing tag. For a fix to the workflow itself, start a new manual run from
`main` and enter the original version tag. Rerunning an old execution uses that
execution's workflow revision. Release tooling comes from the selected workflow
revision, while application code is checked out at the original tag.

An existing published release with no uploaded assets can receive its binaries
if it is mutable, so creating a release through GitHub's UI also works. Completed
published releases are successful no-ops on rerun. Only drafts may have their
assets replaced. Partially populated published releases cannot be rebuilt over
existing files: complete them with the original build artifacts or publish a new
version. Immutable releases missing assets require a new version. Protect `v*` tags
from updates/deletion and enable GitHub immutable releases in repository
settings. Uploading assets to the draft before publication works with immutable
releases. See [GitHub's immutable release documentation](https://docs.github.com/en/code-security/concepts/supply-chain-security/immutable-releases).

Use GitHub's generated release notes to list changes since the previous release.
The release-note configuration groups feature, fix, and maintenance PRs by label.
Version numbering is chosen by the maintainer; tagging and GitHub Releases are
the only publishing operations. The workflow prepares downloadable binaries;
it does not install updates on customer machines or publish Docker images.
The archives are not Authenticode-signed or Apple-notarized; OS trust prompts
remain possible until platform signing credentials are configured.

## Release-only database history

The first published tag captures the complete current schema. Each later tag
uses the greatest versioned **published** GitHub release, including prereleases,
as its parent, downloads its bundle, verifies its published SHA-256 checksum,
and compares that snapshot with the new tag's complete schema. The parent tag
must be an ancestor of the new tag. Versions must increase; release jobs
are serialized. Push one version tag and wait for publication before pushing
the next; GitHub concurrency can replace older pending runs. Draft releases and un-published tags are not database baselines.
Release candidates participate in one linear history, so a stable version follows
its candidates and maintenance release branches are not supported by this pipeline.

The generator works from SQLite's actual catalog rather than only Ent tables.
It retains the raw SQL for constraints, indexes, views and triggers. Changed
ordinary tables are copied transactionally, preserving existing columns, rowids
and AUTOINCREMENT high-water marks. New nullable/defaulted columns and tables
are generated automatically. Triggers are disabled during copying and installed
from the target schema; external-content FTS5 indexes are rebuilt. The generator
applies its program to a disposable database and checks that every resulting
SQLite definition exactly matches the target schema.

Table/column removals and renames, type or primary-key changes, generated-column
rebuilds, virtual-table definition changes, and changed seed statements block
automatic publication. They cannot be inferred safely from snapshots. Constraint
changes can also fail on customer data; the runtime checks them and rolls back.
A successful empty-database check cannot prove that all customer data satisfies
new constraints. Changes to derived projections or business meaning can need
explicit data transformations even when table shapes remain compatible. These
need release-specific implementation and review before publishing; the pipeline
does not guess or silently discard data. Feature development still has no
migration history to manage.

The published bundle contains the cumulative snapshots and SQL steps, and is
embedded in both executables. Customers need neither Atlas nor external SQL
files or network access. Retain release assets permanently: future publications
require the previous bundle. Keep the source placeholder empty in feature commits.
`go run ./cmd/release --version vX.Y.Z --previous /path/to/release-database.json`
can reproduce the preparation step locally; it writes `migrations/release.json`.
Restore that file to `{}` after local release builds; do not commit the generated
bundle. Omit `--previous` for the first release.

## Install or upgrade

Download the archive for your OS/CPU and verify it against `SHA256SUMS` from the
same GitHub release. Extract it to an application directory separate from data.
Set the normal production authentication and storage configuration from the
README. A fresh published binary creates its schema automatically.

For an upgrade:

1. Take a full backup with the currently installed `papergo-admin backup`,
   including the referenced blob files, and keep that binary for recovery.
2. Stop the PaperGo service and wait for its process to exit.
3. Replace the executables with those from the new release, keeping the same
   `DATABASE_PATH`, `BLOB_PATH` and authentication configuration.
4. Restart the service. The new binary acquires the exclusive data lease, saves
   a WAL-consistent SQLite snapshot in `papergo-pre-upgrade-*/database.sqlite`
   beside the database, and applies every pending published step in one
   transaction. Foreign keys and integrity are checked before committing. Only
   then does it start the runner and HTTP server and permit online backups.
5. Check `/health/ready` and the startup logs. Keep snapshots and full backups
   until the new release has been validated; remove them according to your
   retention policy. Startup has a ten-minute limit and requires space for the
   snapshot, changed-table copies, rebuilt search indexes and WAL.

A failed upgrade prevents serving requests and rolls back the whole schema/data
transaction. Logs identify the error and retained snapshot. Unknown schemas,
development databases with edited schema checksums, and database downgrades are
rejected. Skipping releases is supported through the cumulative bundle. Restart
with the same schema is a no-op and creates no extra snapshot. Releases that
share a schema hash need no database changes.

The automatic snapshot contains only SQLite, including runner state. It does not
replace the full database-plus-blobs backup. DBOS maintains its own runner tables
when it starts; those tables are outside the PaperGo schema comparison. A DBOS
migration or later startup failure may happen after the PaperGo schema transaction
has committed. Rolling back an installed application requires stopping it and
restoring the full backup with the matching old `papergo-admin` before starting
the old binary. Backup archives remain tied to their schema checksum; use the
matching release's admin tool to restore them. Do not simply start an older
binary against a database upgraded by a newer schema.
