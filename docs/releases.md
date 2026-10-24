# Releases and database upgrades

A release is a version tag. The release workflow builds, tests and publishes
the binaries and images for it. The database schema changes through
hand-written migrations, which every PaperGo build applies when it starts;
see [Database upgrades](#database-upgrades).

## Publish a version

Merge the desired changes, wait for the `verify` run of that commit on `main`
to pass, then push an annotated tag on it:

```sh
git tag -a v0.1.0 -m 'PaperGo 0.1.0'
git push origin v0.1.0
```

Use `vX.Y.Z` for stable releases and `vX.Y.Z-rc.N` for release candidates.
The release workflow does not run CI again. It first checks that the tag is
newer than the latest published release, has no release yet, and names a
commit whose `verify` run on `main` succeeded. A commit that a newer push to
`main` superseded before its run finished has no such run; tag the newer one.
It then builds and runs native smoke tests for Linux amd64/arm64 and Windows amd64, using [GitHub hosted runners](https://docs.github.com/en/actions/reference/runners/github-hosted-runners). Each archive includes `papergo`,
`papergo-admin`, the optional [MCP server](mcp.md) `papergo-mcp` and this guide. Every executable supports `--version`; version, commit and commit date are injected into the build.
Each platform ZIP includes the executables, native libraries, compiler runtimes, license notices, sample `config.yaml` and operator documentation. Linux libraries live in `lib/`; Windows DLLs sit beside the executables. macOS release and native CI builds are temporarily commented out.

One platform-independent `papergo_vX.Y.Z_assets.zip` contains PaddleOCR, Laya multilingual FP32, EmbeddingGemma 2 Q4 and Harrier Q4 models, tokenizers and model notices. Extract it into the platform installation directory: `assets/` must sit beside the main executable and `config.yaml`. The application finds native libraries and models relative to its executable and loads them directly from disk. The web UI, database schema and migrations remain embedded. Linux requires the target system's glibc; WebP and tokenizers are statically linked. OCR and local decisions use the shared assets automatically. For local embeddings set `AI_EMBEDDINGS_PROVIDER=local` and choose `AI_EMBEDDINGS_MODEL=embeddinggemma-2` or `harrier-oss-v1-0.6b`. Explicit `ONNXRUNTIME_LIB`, `OCR_MODELS`, `AI_DECISION_MODELS_PATH` and `AI_EMBEDDINGS_MODELS_PATH` settings select alternative inference assets. The release downloads and checksum-verifies the models once on Linux, packages one shared ZIP, and tests each platform ZIP with those same assets. The Docker image uses the same directory layout under `/opt/papergo`.

When every build and image test has passed, the workflow creates the GitHub
release with all three platform ZIPs, the shared assets ZIP and `SHA256SUMS`,
and release notes generated since the previous release. Stable releases become
Latest; release candidates are marked as prereleases. No personal access token
is required: the workflow uses GitHub's repository token with
`contents: write` and `packages: write`. Verify Actions is enabled and
organization policy permits those permissions and the native hosted runners.
Linux ARM runners require runner availability for the repository's plan; a
repository without that runner must provide a native ARM runner with an
appropriate label.

If a build or test fails, fix it and tag a new version. After a transient
failure, rerun the failed jobs. The manual dispatch takes an existing tag and
runs the whole workflow again, as long as that tag has no release. Once the
release exists, it is complete: a failure after that, in the image jobs, is
fixed by rerunning those jobs or by a new version. Protect `v*` tags from
updates and deletion, and enable GitHub immutable releases in repository
settings; see [GitHub's immutable release documentation](https://docs.github.com/en/code-security/concepts/supply-chain-security/immutable-releases).

The release-note configuration groups feature, fix, and maintenance PRs by label.
Version numbering is chosen by the maintainer; tagging and GitHub Releases are
the only publishing operations. The workflow publishes downloadable binaries and the container images; it does not install updates on customer machines.
The Windows archive is not Authenticode-signed; OS trust prompts
remain possible until platform signing credentials are configured.

## Container images

Each release also builds the root Dockerfile on native Linux amd64 and arm64 runners. The build injects the version, commit and commit date. Verified shared models are supplied as a separate BuildKit context and image layer. OCI labels identify the source, version and revision.

Each platform's image is pushed by digest with BuildKit SBOM and provenance attestations, then pulled and tested with production local authentication, persistent `/data`, read-only code, health, graceful shutdown, PDF rendering, OCR, Laya and both embedding models. After the GitHub release is created, the workflow tags the tested digests as one multi-platform `ghcr.io/eduardpaul/papergo:vX.Y.Z` (or `vX.Y.Z-rc.N`) with both architectures and their attestations. A stable release also moves `latest`. An untested digest is never tagged; a failed run can leave untagged digests in GHCR.

The optional [MCP server](mcp.md) has its own image, built from `extensions/mcp/Dockerfile` by cross-compiling its pure Go executable for both platforms in one build. After the GitHub release is created, the workflow pushes it by digest with SBOM and provenance attestations, runs its `--version`, and tags it `ghcr.io/eduardpaul/papergo-mcp:vX.Y.Z`, plus `latest` for a stable release.

The package is linked to the source repository. An existing manually created package must grant this repository Actions access. GitHub creates packages as private: set the package Public after first publication if anonymous `docker pull` is intended, or keep it private and authenticate consumers with `read:packages`. See [GitHub's registry instructions](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry) and the [Docker deployment guide](https://github.com/eduardpaul/PaperGo/blob/main/docs/docker.md).

## Database upgrades

The schema masters, `internal/dal/schema/sqlite.sql` and
`internal/dal/schema/postgres.sql`, always describe the current schema, and a
new database gets them directly. An existing database reaches it through
hand-written migrations, one file per engine for each change:

```text
internal/dal/migrations/sqlite/0001_navigation_views.sql
internal/dal/migrations/postgres/0001_navigation_views.sql
```

The schema version is the number of the last migration; it is 0 until the
first one. `schema_metadata` records each database's version: a new database
gets the latest, and each migration moves it on by one. Both engines have the
same file names, numbered from `0001` without gaps. A missing, misnumbered or
unmatched file fails every test.

### Changing the schema

1. Edit both schema masters and regenerate the Bob models
   (`bash scripts/generate-models.sh`).
2. Add the next migration to both engine directories. It takes a database at
   the previous version to the new schema, data included: add and drop
   columns, change constraints, fill or convert values. A migration runs
   inside PaperGo's transaction, so it has no `BEGIN` or `COMMIT`. The README
   in each engine directory shows how that engine changes a `CHECK`; SQLite
   rebuilds the table.
3. Try it on a database at the previous version. For SQLite:

   ```sh
   git show v0.1.0:internal/dal/schema/sqlite.sql | sqlite3 old.db
   sqlite3 old.db "INSERT INTO schema_metadata VALUES (1, 0)"  # v0.1.0's version
   go run ./cmd/admin initialize --database old.db             # applies the migrations
   go run ./cmd/admin initialize --database new.db
   diff <(sqlite3 old.db .schema) <(sqlite3 new.db .schema)
   ```

   A rebuilt table's name shows quoted, as `CREATE TABLE "list_views"`.
   For PostgreSQL, set `DATABASE_URL` to run `initialize` on each database
   and compare their `pg_dump --schema-only`; column order can differ where a
   migration added a column.

Until the first release there are no migrations: the masters change freely
and local databases are recreated. A migration in a published release never
changes; fix it with a new one. An unreleased migration can still change;
recreate local databases that already applied it.

### What happens at startup

Before it starts workers or serves requests, PaperGo compares the database's
version with its own:

- An empty database gets the current schema.
- An older database gets every pending migration, in order, in one
  transaction, followed by the new version. If one fails, nothing changes,
  the log names the file, and PaperGo does not start.
- A newer database is refused: downgrades are unsupported.

On SQLite, PaperGo holds the exclusive data lease while it does this, so no
backup, admin command or other process can use the file. Before migrating it
saves a copy beside the database, `papergo-pre-upgrade-*/database.sqlite`,
with `VACUUM INTO`, which includes the WAL. Migrations run with foreign keys
off, so tables can be rebuilt, and PaperGo checks every reference and the
file's integrity after each one before it commits.
`papergo-admin initialize --database <file>` does the same without starting
the server.

On PostgreSQL, the transaction holds an advisory lock: when several instances
start together, one upgrades and the others then find the database current.
There is no automatic copy, so back up with `pg_dump` first. Stop every
instance of the old version before starting the new one; an old instance does
not know the new schema.

Startup has a ten-minute limit, which covers the copy, rebuilt tables and the
WAL they write.

## Install or upgrade

Download the ZIP for your OS/CPU and the shared assets ZIP from the same release,
and verify both against `SHA256SUMS`. Extract both into the same application
directory, separate from data. Keep the resulting `assets/` beside `config.yaml`
and the executables.
Set the normal production authentication and storage configuration from the
README. The first start creates the database.

For an upgrade:

1. Take a full backup with the currently installed `papergo-admin backup`,
   including the referenced blob files, and keep that binary for recovery.
2. Stop the PaperGo service and wait for its process to exit.
3. Install the executables, native libraries and matching `assets/` from the new
   platform and shared assets ZIPs. Retain your `config.yaml`, `DATABASE_PATH`,
   `BLOB_PATH` and authentication configuration.
4. Restart the service. It upgrades the database as
   [Database upgrades](#database-upgrades) describes, then starts the workers
   and HTTP server and permits online backups.
5. Check `/health/ready` and the startup logs, which report the versions the
   database moved between and the SQLite copy. Keep that copy and the full
   backup until the new release has been validated; remove them according to
   your retention policy.

On PostgreSQL, back up with `pg_dump` in step 1 and stop every instance in
step 2. Skipping releases works: the new build applies every migration since
the database's version. Restarting the same release changes nothing and makes
no copy.

The automatic copy contains only SQLite, including workflow runs and other
operations. It does not replace the full database-plus-blobs backup. A later
startup failure may happen after the migrations have committed. Rolling back
an installed application requires stopping it and restoring the full backup
with the matching old `papergo-admin` before starting the old binary: an old
binary refuses a database that a newer one upgraded. Backup archives record
their schema version, and only an admin tool at that version restores them.
