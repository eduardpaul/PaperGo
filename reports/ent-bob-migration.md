# Ent replacement completion checklist

The objective is complete replacement of ent with Bob, with SQL confined to
`internal/dal`, and the application operating on both DAL schema masters.

- [x] Remove every ent dependency, import, generated entity, schema and tool.
- [x] Move every application SQL statement and query builder into DAL,
      including authentication, identity, search, packages, backup and runner
      integration. Tests outside DAL seed and inspect rows through `daltest`
      helpers and repositories; `TestBobQueryBuildersStayInDAL` guards Bob.
- [x] Run application initialization and shared test fixtures from DAL masters.
- [x] Remove the development migration SQL, Atlas schema configuration and checksums.
- [x] Verify schema fingerprints, model generation, release planning and
      release packaging.
- [x] Implement the cross-table invariants and derived projections through DAL
      transactions; the application masters contain no business-rule triggers.
- [x] Support SQLite and PostgreSQL connection, authorization, runner,
      retention, search, live-event and maintenance paths without fallbacks.
- [x] Preserve exact numeric values, native timestamps and JSON precision.
- [x] Verify concurrent writes and transaction ownership on both engines.
- [x] Run the application tests, API contracts, generation checks and builds
      against the final architecture.

## Layout

Every domain is a package with records, rules and a `Queries` interface,
implemented by `driver/sqlite` and `driver/postgres` with models generated
from the matching schema master. Statements whose shape depends on the request
are built as text for the store's engine in `dialect`, `access`, `content` and
`portable`. No root repository files or shared PostgreSQL models remain; the
root `Store` owns connections, transactions, retries and schema installation.
Each domain package tests its repository on both engines with
`daltest.Engines`.

## PostgreSQL runtime

`DATABASE_URL` (or `database.url`) runs the API on PostgreSQL 17: the schema
is installed into an empty database, the runner keeps DBOS tables in their own
schema, connections disable JIT, and serialization failures are retried.
Foreign keys are deferrable so package imports can load rows in table order.
Storage status reports relation sizes and DBOS run counts on both engines.

## Verification

At the final commit:

- DAL packages pass on SQLite and PostgreSQL 17, including the release
  planner with the pinned sqlite3def.
- `dms`, `httpapi`, `identity`, `runner`, `webdav`, `workflow`, `workflowai`
  and `backup` pass on PostgreSQL 17 with `PAPERGO_TEST_POSTGRES_DSN`, and on
  SQLite.
- Bob model generation for both drivers reproduces the committed models.
- The release rehearsal CI runs passes on Linux: `cmd/release` generates the
  embedded database history, the executables, shared assets and platform ZIP
  are packaged, and `release-smoke.py` exercises the packaged executables.
  The container image builds and `docker-smoke.py` passes against it. The
  release scripts' unit tests pass.
- The API binary passes an end-to-end smoke run on each engine: workspace,
  indexed query, PDF upload and search, a workflow run, storage status and a
  restart that keeps the data.

CI runs the DAL suites and the application suites on PostgreSQL 17.
