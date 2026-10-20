# Data access layer

`internal/dal` owns transactional database invariants using Bob v0.50.0.
`schema/sqlite.sql` and `schema/postgres.sql` define the same 63 tables with
native SQLite and PostgreSQL types. Both schemas use declarative constraints;
repository methods maintain cross-table invariants and derived data in Go.
SQLite JSON columns use TEXT affinity with JSON checks to preserve numeric
scalar payloads exactly.

## Layout

Each domain lives in its own package and is split in two:

- A domain package such as `tasks/` holds the domain's records, inputs and
  invariants once for every engine, and declares the statements it needs as a
  `Queries` interface. It contains no SQL. The domains are `audit/`,
  `blobs/` (file versions and their availability, staged files, previews,
  deletion queue, leases), `catalog/` (field
  definitions, content types, the types collections accept, business
  keys, schema revisions and templates), `delta/` (collection
  synchronization), `directory/` (users, groups, memberships, roles,
  assignments, access tokens, organization settings, preferences, audit snapshots
  and group inboxes), `events/` (domain event log), `grants/` (resource
  access and workspace memberships), `integrations/` (list
  integrations and provider data), `operations/` (background operations),
  `relationships/` (links between items and relationship type policies),
  `resources/` (workspaces, collections, folders and items: their tree,
  permission scopes, deletion, restore, transfer and revision pointers),
  `revisions/` (item revisions, recorded history and publication events),
  `runs/` (workflow runs and step results), `surfaces/` (item surfaces,
  typed field values and group counts), `tasks/`, `taxonomy/` (term
  groups, sets and term trees, keyword resolution and keyword links),
  `views/` (list views and smart folders), `waits/` (workflow waits) and
  `workflows/` (definitions, versions and triggers).
- `refs/` declares the reference checks and the workspace lock every domain
  shares; a domain embeds `refs.Queries` in its own, so drivers implement
  them once.
- `live/` defines change notifications. A domain whose writes notify clients
  embeds `live.Queries` in its own and calls `live.Notify` in the write's
  transaction.
- `driver/sqlite/` and `driver/postgres/` implement those statements with
  models generated for their own engine from the matching schema master. Each
  driver writes the statement its engine runs best; locking is a driver
  method, so SQLite skips row locks its single writer makes redundant.
- `driver/nullable/` converts optional domain values for both drivers' models.
- Some statements are built as text for the store's engine rather than from
  models, because their shape depends on the request. `dialect/` writes the
  expressions that differ between engines and numbers placeholders for
  PostgreSQL. `access/` writes permission checks for a principal. `content/`
  compiles browse, filter, search and link queries over resources.
  `portable/` reads and writes the rows of portable packages for every
  package table, with values that read the same on both engines.
- `dberr/` defines the sentinel errors domains and drivers share.

The root `Store` owns connections, transactions and schema installation,
chooses the driver for its dialect and returns bound repositories such as
`Store.Tasks()` and `Store.Workflows()`. Its `queries` interface lists every
domain contract, so a driver missing a statement does not compile.
`DurableStore` covers the workflow engine's own tables, and
`activitystore/` and `resultcache/` hold extension payloads and cached
answers with SQL both engines run.

### Fixed value sets

A column that holds one of a fixed set of values, enforced by a
`CHECK (column IN (...))` constraint, is a named string type in its domain
package with one constant per value and a `Valid` method, as in
`tasks/values.go`. Never spell these values as string literals in Go:

- Domain records, inputs and filters use the type. Defaults and comparisons
  use the constants, such as `in.Priority = tasks.PriorityNormal`.
- Application boundaries convert once. Request bodies decode into the type,
  query parameters convert with `tasks.Status(q.Get("status"))`, and the
  receiving service rejects values whose `Valid()` is false.
- Drivers convert at their generated models: `string(t.Status)` when writing,
  `tasks.Status(row.Status)` when reading.
- Values placed in `map[string]any` payloads such as events, audit records and
  workflow output are converted with `string(...)`, so consumers can read them
  as strings.
- Keep the type and both schemas' `CHECK` constraints listing the same values.
  Do not use PostgreSQL enum types: SQLite has none, and both drivers'
  generated models must keep the same field types.

To add a domain: add its tables to both driver `bobgen.yaml` files,
regenerate, add the domain package and its driver statements, and embed its
`Queries` in the root `queries` interface. Bob generates a link table, whose
columns are exactly its two foreign keys, only next to both tables it links.

## Connections

Wrap a `database/sql` connection pool with `dal.NewStore(db)` or use
`dal.NewStoreWithDialect(db, dal.DialectSQLite)` / `dal.DialectPostgres`.
Register `modernc.org/sqlite` or `github.com/jackc/pgx/v5/stdlib` with the pool.

SQLite connections must enable foreign keys, a busy timeout and native time
scanning. For example:

```text
file:papergo.db?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_time_format=sqlite&_texttotime=1&_txlock=immediate
```

These settings must apply to every connection, not just the connection used to
create the schema. `ExecTx` reserves SQLite's writer before reading. Transactions
opened outside the store must use `_txlock=immediate` to avoid read-to-write
snapshot upgrade failures. Reads may still use deferred, read-only transactions.

`Store.Migrate(ctx)` installs the embedded dialect schema atomically in a fresh
database. It is not an upgrade mechanism or an idempotent startup check. The
schema masters are `internal/dal/schema/sqlite.sql` and
`internal/dal/schema/postgres.sql`. Edit these files directly. Initialization,
test fixtures and release tooling must consume the selected DAL schema;
`migrations/` and `ent/schema` do not define it.

## Transactions and queries

Multi-step mutations run in `Store.ExecTx`. Use the callback's `bob.Executor` for
every query inside that mutation. `Store.WithTx(tx)` joins a caller-owned SQL
transaction; the caller commits or rolls back that whole transaction on error.
Repository callbacks must not perform external side effects.

PostgreSQL resource mutations lock their workspace rows in deterministic order.
Directory graph mutations use a transaction-scoped advisory lock, and taxonomy
hierarchy mutations lock their term set. These locks serialize checks whose
invariants span multiple rows, including last-owner and cycle checks. SQLite's
single writer provides the corresponding serialization.

Live notifications advance `live_change_position` in their mutation transaction.
Its singleton row serializes position allocation until commit, so a stream cursor
cannot pass an uncommitted change. The committed position survives journal
retention. PostgreSQL content sort columns use the `C` collation, matching
SQLite's binary text order and Go's merge of head and published pages.
Case-insensitive matching lowers text with the built-in `pg_c_utf8` collation,
which folds every script whatever the database locale, as SQLite's
`unicode_lower` and Go's `strings.ToLower` do.

SQL and Bob query construction belong inside this module. Application packages
call typed repository methods and pass filters; they do not execute queries or
construct Bob setters. `dal/activitystore` owns durable payload SQL, references
and leases. Raw SQL inside DAL must support the selected engine.

Use `dberr.Translate`, `dberr.IsNotFound`, `dberr.IsConstraintError` and
`errors.Is` with the sentinel errors defined in `dberr`.

## Resource projections

Repository writes keep permission scopes, keywords, surface summaries and
single-choice facets consistent with resource mutations:

- Collection moves within a workspace transfer surface and field-value locations, business-key
  claims and counts while retaining immutable revision provenance. A conflicting
  business key rolls back the complete move.
- Soft deletion marks live descendants together, removes their business-key
  claims and subtracts their surface counts. Frozen surface and field-value
  projections are retained for restoration; `QueryDynamicSurface` excludes
  tombstones and returns each matching surface once.
- Restoration requires a live parent, collection and workspace and adds the
  retained projections back into counts. Business keys must be claimed as part
  of the caller's restore transaction when required by its schema.
- Purge deletes descendants before ancestors, updates counts and emits item
  deletion notifications in the same transaction.
- Revision allocation advances the resource head, values and history atomically.
  New revisions, publications and surface projections reject tombstones.

Keyword resolution follows merged IDs to an active, eligible canonical term,
deduplicates the result, and only creates free-text terms in an open keyword
set. Database lookup failures are propagated. Merges require active terms.

## Tasks

Task creation, responses, reassignment and cancellation run through
`Store.Tasks()`, which returns plain `tasks.Task` records. A version check and task/workspace locks prevent two answers from
completing the same task concurrently. All-mode tasks count only currently
assigned entries; ties choose the first configured outcome, and decisive answers
complete immediately. Reassignment can settle a task in the same update.

Task outcome JSON is generated as `[]model.TaskOutcome` in both drivers'
models, preserving form and comment rules. Task updates also require the
version they read, so a lost lock surfaces as `ErrConcurrentUpdate`.
The task API builds its explicit response model and keeps authorization, form
validation, staged-file review, audit and domain events in its DMS transaction.
Assignee and waiting-list queries expand JSON using the selected database engine
and bind recipients inside the repository.

## Workflow waits

Wait creation, completion and claims run through `Store.Waits()`. Creation
uses a conflict-safe insert and validates run/workspace containment. Replaying an
execution retains its original data; changing ownership, node or payload
identity conflicts. Closed waits cannot reacquire parking or retention.
Conditional claim updates fence competing holders, refresh tokens for the same
holder and recover expired claims. Completion clears all claim fields and
accepts equivalent JSON on replay without rounding large numbers.

The workflow API keeps payload references, blob leases, consumption,
cancellation and notification writes inside its application transaction.
Activity record SQL uses parameters supported by both engines; immutable writes
and compare-and-swap compare JSON values independently of database formatting.

Workflow execution reads immutable definitions through DAL repositories.
`Waits().Authority` resolves the original version author and leased file
sources for AI wait authorization without loading run-status fields. Permission checks remain in the application,
including current access to the source item and each retained file.

Event origins, audit targets, run item memberships and retry origins store
immutable identifiers. They retain their identity after content purge or run
retention. Repositories validate current item and retry ownership at creation;
these provenance fields do not use foreign keys that would erase history.

## Model generation

Update both dialect schemas together. Models are generated from the schema
masters by `bobgen-sql`, a tool in `go.mod`, so the generator uses the same
SQLite build as the application. Register typed JSON replacements in each
`bobgen.yaml` that generates a JSON column; SQLite stores JSON as `TEXT`, so
its driver needs the replacement as well.

Run from the repository root with Docker available (PostgreSQL generation
loads the schema into a temporary container):

```sh
bash scripts/generate-models.sh
```

Delete generated files for removed tables in the same change.

## Verification

```sh
go test -race ./internal/dal/...

TEST_POSTGRES_DSN='postgres://user:pass@localhost:55433/papergo?sslmode=disable' \
  go test -race ./internal/dal/... -count=1
```

Each domain package tests its repository next to its code, on both engines:
`daltest.SQLiteStore` and `daltest.PostgresStore` open a store with the
schema, and `daltest.Engines` runs a test once per engine as the subtests
`SQLite` and `Postgres`. `daltest.NewFixture` and the resource helpers build
the shared workspace, libraries and item the domain tests start from. The
root package tests only the `Store` itself.
Tests do not install the schema each time. SQLite tests copy a database file
migrated once per test binary; PostgreSQL tests clone a template database
migrated once per test binary with `CREATE DATABASE ... TEMPLATE` and drop
their clone afterwards, so the supplied user needs permission to create
databases. Each package's `TestMain` calls `daltest.Main`, which drops the
template. Without a DSN, PostgreSQL tests skip. Run the PostgreSQL tests
alone with `-run Postgres`; the SQLite tests do not depend on the DSN.
CI provides PostgreSQL 17 and runs both dialect suites with race detection.

Application suites run on SQLite by default. With
`PAPERGO_TEST_POSTGRES_DSN` set, `daltest.Database` and its siblings create
each test's database on that server instead: they clone a fixture database
built once per test binary and drop the clone afterwards. Fixture databases
an interrupted run leaves behind are dropped by a later run after a day.

```sh
PAPERGO_TEST_POSTGRES_DSN='postgres://user:pass@localhost:55433/papergo?sslmode=disable' \
  go test ./internal/dms ./internal/httpapi ./internal/runner -count=1
```

`bash scripts/check.sh --postgres` runs the DAL and application suites this way
on the disposable server of `scripts/postgres.sh`.

Tests seed and inspect rows through `daltest` (`Insert`, `Count`, `Values`,
`Update`, `Delete`, `Reject`), which run on either engine; tests outside DAL
issue no SQL of their own.
