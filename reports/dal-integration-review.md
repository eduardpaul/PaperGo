# DAL integration review

The Bob DAL has shared SQLite/PostgreSQL tests for resource hierarchies,
projections, revisions, directory graphs, grants, taxonomy and relationships.
The application integration remains incomplete; passing the DAL suite does not
establish that the application runs on PostgreSQL.

## Remaining integration work

1. **Application database ownership and callers.** `internal/dal/database.Database`
   and `internal/dms.Service` construct ent clients. DMS, workflow and runner methods expose ent models and enums. Replace those calls and public
   response types directly with repositories and explicit API models; remove
   generated ent code and its dependencies as part of that cutover.

2. **Startup and release verification.** Initialization and fixtures install
   the selected DAL master and validate its `schema_metadata` fingerprint.
   Database lifecycle, release planning and release metadata live under DAL.
   Continue end-to-end validation while replacing remaining DMS and identity
   callers; raw-SQL trigger assertions must test repository behavior instead.

3. **Raw SQL and database operations.** Application queries use SQLite `?`
   parameters, `json_each`, `sqlite_sequence`, `sqlite_master`, pragmas and custom
   SQLite scalar functions. Connection opening, live cursors, maintenance,
   backup, restore and allocation reporting need explicit engine support.
   PostgreSQL live cursors also need to account for sequence allocation outside
   transactions and transactions committing in a different order from their IDs.

4. **Exact numeric projections.** `field_values.value_numeric` uses SQLite
   NUMERIC affinity, which can coerce decimal strings to floating point. Native
   PostgreSQL NUMERIC does not have that precision limit. The application uses
   sortable exact numeric text keys; choose a single exact representation before
   migrating numeric query, grouping and sorting paths. Avoid keeping alternate
   representations for compatibility.

5. **Complete mutation coverage.** Remaining repositories need caller-level
   integration and concurrent validation tests, especially JSON field references,
   blob object consistency, workflow deletion/run/task cleanup, retention and
   revision publication. The generated setters expose low-level writes; calling
   them alone does not enforce domain invariants.

## Master schema requirements

The DAL schemas define 62 tables with declarative foreign keys and checks and
no triggers. Cross-table invariants, projections, lifecycle rules and change
notifications must execute in repository transactions. In particular:

- `workflow_runs.live_updated_at`, `item_surfaces.item_created_at` and
  `item_surfaces.modified_at` are native timestamps. Callers must use time values
  and timestamp ordering.
- Waits and blob leases reference real owning runs; run/workspace containment
  also needs repository validation.
- Identity, taxonomy, resource and workflow foreign keys require transactionally
  ordered cleanup and creation.
- Defaults and nullability come from the DAL masters. Bob's PostgreSQL models
  use the SQLite master's defaults when normalizing generated INSERTs.

## Verified behavior

- Generated INSERT defaults work on SQLite for single rows and heterogeneous
  batches, including generated IDs and nullable values.
- Repository-owned SQLite writes reserve the writer before reading; PostgreSQL
  workspace, directory and term-set locks protect the covered concurrent checks.
- Moves, deletion, restore and subtree purge maintain projections and counts
  atomically. Failed moves roll back their resource and projection changes.
- Last-owner removal and user deletion preserve active workspace ownership.
- Keyword resolution canonicalizes merge chains and rejects ineligible terms;
  lookup errors are propagated.
- Organization-audit reads use Bob with an explicit API response model.
- Task persistence, assignee queries and API views use Bob; `internal/tasks` has
  no ent imports. Responses, reassignment and cancellation are transactional and
  version checked, with shared tests on both database engines.
- Workflow wait storage and notification writes use Bob. Creation handles replay
  without aborting PostgreSQL transactions, validates run/workspace containment,
  and preserves execution identity. Shared tests cover completion precision,
  concurrent claims, token refresh, expiry and caller-owned rollback.
- Migrated tasks, waits, audit, forms, staged-file checks and blob-text callers
  use typed DAL repositories. Their SQL and Bob query construction stay inside
  `internal/dal`; durable activity storage lives in `internal/dal/activitystore`.
- Blob leases use the DAL executor. Activity record storage uses portable SQL;
  shared tests cover JSON replay, compare-and-swap, lease fencing and retention.
- Workflow execution reads its metadata and immutable definitions through DAL
  repositories. AI dispatch resolves the owning version's author and retained
  source files through DAL, then applies application permission checks.
  `internal/workflowai` and `internal/workflow/run.go` have no ent imports.
- PostgreSQL tests use private schemas and leave `public` untouched. CI runs
  SQLite and PostgreSQL 17 with race detection.
