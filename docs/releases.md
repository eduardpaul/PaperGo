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
bundle. It builds and runs native smoke tests for Linux amd64/arm64 and Windows amd64, using [GitHub hosted runners](https://docs.github.com/en/actions/reference/runners/github-hosted-runners). Each archive includes `papergo`,
`papergo-admin`, the database bundle and this guide. Both executables support
`--version`; version, commit and commit date are injected into the build.
Each platform ZIP includes the executables, native libraries, compiler runtimes, license notices, sample `config.yaml` and operator documentation. Linux libraries live in `lib/`; Windows DLLs sit beside the executables. macOS release and native CI builds are temporarily commented out.

One platform-independent `papergo_vX.Y.Z_assets.zip` contains PaddleOCR, Laya multilingual FP32, EmbeddingGemma 2 Q4 and Harrier Q4 models, tokenizers and model notices. Extract it into the platform installation directory: `assets/` must sit beside the main executable and `config.yaml`. The application finds native libraries and models relative to its executable and loads them directly from disk. The web UI and database schema remain embedded. Linux requires the target system's glibc; WebP and tokenizers are statically linked. OCR and local decisions use the shared assets automatically. For local embeddings set `AI_EMBEDDINGS_PROVIDER=local` and choose `AI_EMBEDDINGS_MODEL=embeddinggemma-2` or `harrier-oss-v1-0.6b`. Explicit `ONNXRUNTIME_LIB`, `OCR_MODELS`, `AI_DECISION_MODELS_PATH` and `AI_EMBEDDINGS_MODELS_PATH` settings select alternative inference assets. CI prepares and checksum-verifies the models once on Linux, packages one shared ZIP, and tests each platform ZIP with those same assets. The Docker image uses the same directory layout under `/opt/papergo`.

Publication creates a draft, uploads all three platform ZIPs and the shared assets ZIP plus
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
published binary releases retain their assets on rerun. Image publication is checked separately, so reruns can finish a missing GHCR image after the binary release has been published. Only drafts may have their
assets replaced. Partially populated published releases cannot be rebuilt over
existing files: complete them with the original build artifacts or publish a new
version. Immutable releases missing assets require a new version. Protect `v*` tags
from updates/deletion and enable GitHub immutable releases in repository
settings. Uploading assets to the draft before publication works with immutable
releases. See [GitHub's immutable release documentation](https://docs.github.com/en/code-security/concepts/supply-chain-security/immutable-releases).

Use GitHub's generated release notes to list changes since the previous release.
The release-note configuration groups feature, fix, and maintenance PRs by label.
Version numbering is chosen by the maintainer; tagging and GitHub Releases are
the only publishing operations. The workflow publishes downloadable binaries and the container image; it does not install updates on customer machines.
The Windows archive is not Authenticode-signed; OS trust prompts
remain possible until platform signing credentials are configured.

## Container images

Each release also builds the root Dockerfile on native Linux amd64 and arm64 runners. The build embeds the same `release-database.json` as the platform binaries and injects the version, commit and commit date. Verified shared models are supplied as a separate BuildKit context and image layer. OCI labels identify the source, version and revision.

Candidate images are uploaded by digest with BuildKit SBOM and provenance attestations. Each exact digest is pulled and tested with production local authentication, persistent `/data`, read-only code, health, graceful shutdown, PDF rendering, OCR, Laya and both embedding models. Only after the binary and image checks pass and GitHub Releases publication succeeds does the workflow expose the tested images as `ghcr.io/eduardpaul/papergo:vX.Y.Z` (or `vX.Y.Z-rc.N`). The multi-platform index includes both native architectures and their attestations. Only the current stable GitHub release can update `latest`; retrying an older release cannot move it backwards.

An existing version image is checked for its source revision and both platforms and is never replaced. A missing image can be completed by rerunning the release workflow; a published database bundle is downloaded and checksum-verified for that image-only retry. Authentication errors fail the workflow instead of being treated as absent tags. A failed image build can leave untagged candidate digests in GHCR; these are not release version tags.

The registry jobs use `GITHUB_TOKEN` with `packages: write`; inspection uses `packages: read`. The package is linked to the source repository. An existing manually created package must grant this repository Actions access. GitHub creates packages as private: set the package Public after first publication if anonymous `docker pull` is intended, or keep it private and authenticate consumers with `read:packages`. See [GitHub's registry instructions](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry) and the [Docker deployment guide](https://github.com/eduardpaul/PaperGo/blob/main/docs/docker.md).

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

Download the ZIP for your OS/CPU and the shared assets ZIP from the same release,
and verify both against `SHA256SUMS`. Extract both into the same application
directory, separate from data. Keep the resulting `assets/` beside `config.yaml`
and the executables.
Set the normal production authentication and storage configuration from the
README. A fresh published binary creates its schema automatically.

For an upgrade:

1. Take a full backup with the currently installed `papergo-admin backup`,
   including the referenced blob files, and keep that binary for recovery.
2. Stop the PaperGo service and wait for its process to exit.
3. Install the executables, native libraries and matching `assets/` from the new
   platform and shared assets ZIPs. Retain your `config.yaml`, `DATABASE_PATH`,
   `BLOB_PATH` and authentication configuration.
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
