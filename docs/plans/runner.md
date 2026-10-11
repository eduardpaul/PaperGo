# Async runner (plan)

Status: **Phase 1 (core) implemented.** The runner runs inside the API process and executes workflows v1 ([`docs/workflows.md`](../workflows.md)). Phases 3 to 6 below are planned. Notifications, tasks and approval build on workflows as activities and built-in workflows.

Reviewed against `main` at `f377d1f`. Engine facts were checked in the source of [DBOS Transact Go](https://github.com/dbos-inc/dbos-transact-golang) v1.6.0 (2026-10-07).

## Purpose

PaperGo has no asynchronous machinery. It has no outbox, worker, schedule or durable wait. Everything happens inside one request and one `dms.Service.write` transaction (`internal/dms/service.go:31`). The runner adds one model for everything that happens *after* something or *on a schedule*:

- reactions to domain events (an item was published, a task was completed);
- background jobs (long rebuilds, imports, extraction);
- cron schedules (retention, reconciliation, reminders);
- durable waits with timeouts (approvals, escalations);
- retries with backoff and visible failures.

It must keep PaperGo's minimal install (one process, one SQLite file) and also run unchanged on several nodes once PaperGo has a PostgreSQL adapter.

### Workflows are the construction core

The runner is the engine; **workflows** are what people build with. Every process that happens after a change or on a schedule is a workflow definition that workspace managers can see, change, turn off or replace, including the processes PaperGo itself ships ("built-in workflows"). New product features that react to changes are delivered as workflow *activities* and built-in workflows, never as hidden code paths. The model follows the PaperDotNet design (definitions with triggers, a condition and a flow of activity nodes connected by outcome ports; immutable versions; runs pinned to their version), mapped onto DBOS:

- One registered DBOS workflow, `papergo.run`, interprets a pinned definition version. Each node runs as one durable step, so a run resumes at the node it reached; waits and delays are DBOS `Sleep`/`Recv`.
- Writes only append their domain events to a durable log in their own transaction. A dispatcher, on whichever node claims the events, matches them to workflows and queues the runs; any node's workers execute them. The write path never matches triggers or evaluates conditions, and execution is not tied to the server that made the change.
- The user documentation of the definition model is [`docs/workflows.md`](../workflows.md).

## Engine: DBOS Transact Go

The runner embeds [DBOS Transact Go](https://github.com/dbos-inc/dbos-transact-golang) (MIT). DBOS checkpoints workflows and their steps in a *system database*. A process that crashes resumes each pending workflow from its last completed step.

| Option | Assessment |
| --- | --- |
| **DBOS Transact Go** (chosen) | Pure-Go SQLite (`modernc.org/sqlite`, the driver PaperGo already uses, CGO-free) and PostgreSQL. **Enqueue and send inside the caller's own `*sql.Tx`** (`WithEnqueueTransaction`, `WithSendTransaction`), so no outbox is needed. Built-in cron (`CreateSchedule`), queues with concurrency, rate limits, partitions, priority, delay and deduplication, `Send`/`Recv` with timeout, durable `Sleep`, and workflow IDs as idempotency keys. It links pgx, puddle, pgpassfile, pgservicefile, pgerrcode, robfig/cron/v3 (MIT) and gorilla/websocket (BSD-2-Clause, for the Conductor client). The commercial Conductor service is optional and not used. |
| go-workflows (MIT) | Temporal-style replay with SQLite and Postgres backends. It cannot start a workflow inside our transaction (an outbox and relay are needed) and has no cron. Its migrations pull in MPL-2.0 packages (`hashicorp/go-multierror`, `errwrap`) through golang-migrate. |
| Dapr durabletask-go (Apache-2.0) | The engine behind Dapr Workflows, embeddable, with SQLite and Postgres. Replay model, with a gRPC/protobuf/dapr-kit stack, no cron and no transactional start. The full Dapr Workflows needs the Dapr sidecar, which breaks the one-container install. |
| Temporal | The most mature, but needs a separate server cluster and its own database. |
| River | Postgres-first job queue, MPL-2.0. |

Compared with PaperDotNet (outbox messages, a run table and bookmarks; ADR-0019/0036), DBOS gives the same guarantees without building them: atomic "save and start", idempotent restarts and resumable waits.

## Topology

- `internal/runner` is the only package that imports DBOS. `internal/dms` raises domain events through its `Events` interface, and `internal/workflow` (definitions, matching, the interpreter) depends only on an `Exec` interface for durable steps and sleeps, so the engine stays replaceable.
- Every API process is also a worker on the `workflows` queue (4 concurrent runs per node). A later `RUNNER_QUEUES` setting can split API and worker roles.
- **SQLite:** DBOS keeps its system tables in the **same database file** as PaperGo, so starting work is atomic with the domain write. This stays one node, as the [deployment rules](../../README.md#deployment-and-scaling) already require.
- **PostgreSQL** (with the future PostgreSQL adapter): the DBOS `dbos` schema goes in the same database. Any number of nodes share queues; DBOS dequeues with `SKIP LOCKED`.

## Database integration

The phase 0 spike proved this design; the numbers are in [Spike results](#spike-results).

- **Write transactions lock when they begin.** `database.DefaultOptions` (used by `database.Open`) sets `_txlock=immediate` and a 30-second `busy_timeout`. modernc keeps starting read-only transactions deferred, so reads keep their WAL snapshots. Write transactions take the write lock at `BEGIN` and wait their turn, instead of failing with `SQLITE_BUSY` when a deferred transaction tries to upgrade after DBOS has written. `writeMu` stays as the in-process queue for request writes; workflow steps wait on the SQLite lock.
- **DBOS has its own handle on the same file.** `internal/runner` opens a second `*sql.DB` with `database.DSN`, the helper `database.Open` uses, so both handles have the same pragmas, and passes it as `Config.SQLiteSystemDB`. DBOS `Shutdown` closes the handle it was given, so it must never be PaperGo's pool.
- **One transaction for Ent and the event log.** `write()` begins a `*sql.Tx` and builds the Ent client over it, with a driver whose nested transactions join it. The same transaction carries domain rows, audit rows and the write's `domain_events` rows. The dispatcher's transaction carries the `workflow_runs` rows, the queued DBOS runs (DBOS accepts the `*sql.Tx` from PaperGo's pool because both handles open the same database) and the events' dispatched mark. Were the engine's tables ever in another database, the dispatcher would queue first (idempotent by run ID) and mark after.
- **Reserved table names.** DBOS adds 13 tables without a prefix: `application_versions`, `dbos_migrations`, `event_dispatch_kv`, `notifications`, `operation_outputs`, `queues`, `streams`, `workflow_events`, `workflow_events_history`, `workflow_input`, `workflow_output`, `workflow_schedules`, `workflow_status`. PaperGo tables must not use these names; for example, a future inbox becomes `user_notifications`.
- **Migrations.** DBOS migrates its own tables, versioned in `dbos_migrations`. This is the one documented exception to the single Atlas file in [AGENTS.md](../../AGENTS.md): PaperGo never edits those tables. Atlas applies the PaperGo schema to a fresh file first (`CheckSchema` refuses to start without it); DBOS migrates its tables when the runner launches, and the API does not start if that fails. Multi-node deployments run DBOS migrations once, before nodes start (`SkipMigrations` on the nodes).
- **Step completion markers.** The schema file has one runner table, `runner_step_results (run_id, step_id, step, output)`, for exactly-once steps (below). Only the runner reads it, with plain SQL, so it has no Ent model.

## PaperGo contract

| Piece | Where | Meaning |
| --- | --- | --- |
| Domain event log | `dms` writes | Every item write appends `item.created`, `item.updated`, `item.published`, `item.unpublished` or `item.deleted` (`dms.Event`, UUIDv7 IDs) to `domain_events` in its own transaction, and nothing else. A rollback raises nothing. `Service.Emit` adds manual starts, schedule occurrences and workflow events (`wf.{key}.{event}`) to the same log. After a commit that logged events, `dms` calls the runner's `Wake`. |
| Dispatcher (`Runner.Dispatch`) | runner, any node | Takes undispatched events oldest first in batches of 100. `workflow.Match` finds the workflows whose triggers and condition accept each one, records `workflow_runs` rows, and queues `papergo.run` with workflow ID `wf:{workflowID}:{eventID}`; the batch's dispatched mark commits with them. A crash dispatches a batch again or not at all, and run IDs make repeats harmless. It wakes on `Wake` and polls every second for events that other nodes or a restart left. On PostgreSQL, dispatchers on several nodes claim batches with `FOR UPDATE SKIP LOCKED`. |
| Exactly-once step (`stepTx`) | each workflow node | The node's writes through `dms` (as the version's author), the events they raise, the runs those start, and a `runner_step_results` row keyed by run ID and DBOS step ID commit in one transaction. A retried or recovered step finds the row and returns the stored output. Replay assigns step IDs in the same order, so a node can run many times in a run (loops, retries); the recorded name must match on replay. The output is always returned decoded from the row, so a first run and a replay see the same value. DBOS `RunAsTransaction` cannot be used: its callback gets a DBOS `Tx` without the `*sql.Tx`, so Ent cannot run on it. Lock waits that outlive the busy timeout are retried three times. |
| Durable sleep | `delay` nodes, retry delays | DBOS `Sleep`, which survives restarts. |
| Schedules | `Launch` | DBOS database schedules: `papergo.tick` every minute raises due workflow schedule triggers, and `papergo.retention` daily at 03:30 UTC deletes old runs. DBOS fires each tick once across nodes. |
| Signals and waits (phase 3) | approvals | `Send` in the deciding request's transaction with an idempotency key, `Recv` with a timeout in the run. The workflow must already exist: DBOS rejects a message to an unknown workflow ID. A message sent before the run reaches `Recv` is kept. |

Rules:

- **Event types are a contract.** [`docs/workflows.md`](../workflows.md) documents each type's data. Events are separate from `audit_events`: audit is the manager's record of what changed; events are what workflows react to.
- **Runs act as the version's author.** Every step authorizes as the subject who saved the workflow version, as a request would, so a workflow never does more than its author may. Events a run raises are one level deeper than the event that started it, and depth 5 starts nothing.
- **Queues:** one `workflows` queue today. Partitioned `bulk` and serial `maintenance` queues come with phases 3 and 4.

## Semantics

- **Delivery.** Every workflow node is an exactly-once step. Starts are atomic with the transaction that triggers them.
- **Failures.** A node follows its own retry policy (durable sleeps between attempts), then its `error` port, else the run ends in `ERROR` and raises `wf.{key}.failed`. A run that keeps crashing its process exceeds its maximum recovery attempts and is parked. Both show as `failed` in the run API; a failed run can be retried from its failed step (DBOS `ForkWorkflow`).
- **Workflow code is replayed.** After a crash, DBOS reruns the workflow function and returns recorded step results. Review checklist for workflow functions (steps are exempt):
  - no `time.Now`, randomness, map-order dependence or I/O outside steps;
  - no reads of mutable globals;
  - change running logic only behind `runner.Patch` (DBOS `Patch`/`DeprecatePatch`);
  - keep inputs and outputs JSON-serializable and small.
- **Versions.** By default DBOS stamps each workflow with a hash of the binary and recovers only workflows from the same binary, so a deploy would strand pending work. PaperGo fixes the application version (`papergo-1`). The interpreter's step sequence (`load`, one step per node attempt, `finish`) is the compatibility contract; an incompatible change to it must be guarded with DBOS `Patch`.

## Product processes as system workflows

PaperDotNet moved every product reaction to saved items into workflows ([PR #9](https://github.com/eduardpaul/PaperDotNet/pull/9), ADR-0047), and found what the engine needs once each item change starts several product runs. PaperGo adopts the same model in phase 3, before its first product process (search indexing, document text, notifications) is built:

- **System built-ins.** A product process is a built-in with flags:
  - `system`: own queue, so a rebuild of thousands of items never delays people's workflows; successful runs kept about 24 hours; starts past the depth limit (hard cap 20), because its activities guard their own loops.
  - `required`: its role always has an active workflow.
  - `locked`: a product guarantee, such as search removal or permission-scope updates. It cannot be replaced, copied, turned off or deleted.
  - `include_folders`: folder events start it too.
  - `lightweight`: solution reactions with system costs but no role.
- **Roles, not keys.** A process is a role, such as `search.index` or `documents.text`.
  - Built-ins, their copies and alternatives fill a role through `provides`, with one active per scope (workspace or collection). Turning one on turns the others off.
  - A workflow that fills a role raises the role's events (`wf.{role}.…`), so a customized "read the text" still feeds everything that follows `wf.documents.text.hasText`.
  - Consumers ask for the role, never a built-in key.
  - Roles belong to the core only: collections, search, document processing and notifications. Solutions ship ordinary built-ins.
- **Contracts that fixed code enforces.** A custom pipeline can change quality and cost, never permissions or inclusion. For example, a search-index workflow stages chunks, and a fixed `search.publish` activity checks revision, policy and permissions.
- **Engine-owned item context.** The item event that started a run is the run's trigger data, written by the dispatcher. Data raised by `event.raise` or a manual start can never pose as an item change. PaperGo already has this: `wf.*` and `manual` events are distinct types.
- **Cheap when nobody listens.** A finished run raises `wf.{key}.completed`/`failed` only when a workflow of the workspace listens to it, and trigger content-type filters avoid runs entirely.
- **Coordination without polling.** A bounded fan-out (`requests`) starts child runs with IDs deterministic per step, waits for their completion as a durable wait, tolerates failures, and yields after a bounded amount of work.

## Multi-node

On PostgreSQL several nodes share queues and schedules safely. Two pieces are PaperGo's responsibility:

- **Node identity.** Each node has a stable `NODE_ID`, used as the DBOS executor ID. A restarted node recovers its own pending workflows.
- **Recovering a node that never returns.** Nodes heartbeat into `runner_nodes`. The holder of a `runner_leases` row takes over the pending workflows of a node whose heartbeat expired: it lists them (`ListWorkflows` filtered by that executor ID and `PENDING`) and calls `ResumeWorkflows`, which puts them back on the internal queue, where the live nodes run them. A node that was only slow may still be running one of them; step markers keep its writes single.

Other in-process state also needs shared implementations before a second node: WebDAV locks (`internal/webdav/locks.go`), and later the live SSE hub (PostgreSQL `LISTEN/NOTIFY`).

## Review of `main`: what the runner serves today

Existing behaviour that the runner can take over or complete:

| Area | Today on `main` | With the runner |
| --- | --- | --- |
| Index and business-key rebuilds | `UpdateField` (`internal/dms/schema.go:120-129`) and `ApplyTemplate` (`internal/dms/templates.go:252-256`) call `rebuildSurfaces`/`rebuildBusinessKeys` in pages of 500 (`surfaceBatch`, `internal/dms/schema.go:199`), all inside the request's write transaction. A large collection holds the single writer for the whole rebuild. | A `bulk` job, partitioned by collection, of resumable exactly-once batch steps. This needs a defined consistency contract while a rebuild is pending, for example the collection reports `index_pending` and indexed queries over the affected field are rejected until it finishes, as [the architecture](../architecture.md) requires for any asynchronous projection. |
| Content-type validation of existing heads | `checkTypeHeads` (`internal/dms/contenttypes.go:226`) scans every item of a type in the request. | Stays synchronous while it is a precondition of the change. A job is only worth it if validation becomes advisory. |
| Switching a collection to automatic publishing | `publishAllHeads` (`internal/dms/revisions.go:357`), called from `internal/dms/service.go:378`, publishes every unpublished head in one transaction. | The same partitioned `bulk` job pattern. |
| Orphaned blobs | A crash between writing the object and committing metadata leaves an unreferenced file. Ordinary failures clean up best-effort (`internal/httpapi/api.go:493`, `internal/webdav/methods.go:92`). The README asks deployments to reconcile orphans after a grace period. | A built-in maintenance workflow that compares stored objects with `blobs` and deletes unreferenced objects older than the grace period. |
| Expired WebDAV app passwords | Rejected at use (`internal/dms/credentials.go:95`) but never removed. | A built-in maintenance workflow purges expired credentials. |
| Recursive WebDAV COPY | Not one transaction, capped at `MaxFolderEntries` (`internal/webdav/methods.go:231-265`). | Optionally, a durable copy that resumes after a crash. The WebDAV request still has to answer synchronously, so this is a later improvement. |
| Subtree delete, permission break/copy/reset | Immediate and transactional (`internal/dms/service.go:459`). | **Stays synchronous.** Revocation must be immediate ([architecture](../architecture.md)); the runner would only add follow-up reactions. |
| Smart-folder import, bulk operations | Atomic, bounded (100 items or definitions). | Stays synchronous. A background import is only needed for packages beyond the bounds. |

New capabilities the runner unlocks. Each will be designed separately:

- notifications (inbox and live events);
- tasks and approval of publishing;
- an item change feed and change subscriptions (the [PaperDotNet comparison](../paperdotnet-comparison.md) §3);
- document text extraction and OCR projections;
- audit and revision retention rules.

## Operations

- **Runs API.** Workspace managers list a workspace's runs, read a run with its steps, cancel, and retry failed runs (see [`docs/workflows.md`](../workflows.md#api)). A deployment-wide view of runs across workspaces needs a deployment-level role, which PaperGo does not have yet.
- **Retention.** The `papergo.retention` schedule deletes dispatched events and the runs that finished more than `RUN_RETENTION` ago. It first deletes the runs' `runner_step_results` and `workflow_runs` rows, then the runs (`DeleteWorkflows`). In that order, a crash in between leaves only finished runs without markers, which never replay, and markers never outlive their run.
- **Configuration:**

  | Setting | Meaning |
  | --- | --- |
  | `NODE_ID` | Default `local`; required and unique per node on PostgreSQL |
  | `RUN_RETENTION` | How long finished runs are kept (default `720h`) |

- **Lifecycle.** `cmd/api/main.go` creates the runner (which registers its DBOS workflows and makes `dms` wake its dispatcher), calls `Launch` before serving (DBOS migrations, recovery of this node's runs, schedules, built-in sync, the dispatcher), and shuts it down after the HTTP server's graceful stop. Events logged while no runner was running are dispatched when one launches. Readiness fails when the runner has stopped.
- **Observability.** Structured `slog` logs per failed run; DBOS logs through the same logger. Metrics and tracing export follow the general observability work.

## Phases

1. **Spike** (done). See [Spike results](#spike-results).
2. **Core** (done). The runner embedded in the API process (immediate transactions, domain events from every item write, exactly-once steps, run retention), and workflows v1: definitions and versions per workspace, item and schedule and manual triggers (manual ones per item or over an ordered selection, after PaperDotNet [PR #8](https://github.com/eduardpaul/PaperDotNet/pull/8)), conditions in the query filter language, the flow interpreter with the item activities, built-in workflows with the first one (unpublishing expired items), and the workflow and run API.
3. **Workflow parts and system workflows.** `forEach` (including over `{run:items}`), approvals (durable `Recv` waits) whose answers use the same forms as launches (`input_schema`, domain pickers checked against the decider), concurrency policies (`skip` for sweeps; they compare every member of selection runs, from `workflow_run_items`), run-again activities, and the system-workflow model below; then the maintenance processes as system built-ins (credential purge, orphan reconciliation).
4. **Long work.** Move index and business-key rebuilds and `publishAllHeads` to partitioned jobs, with the pending-index contract.
5. **Multi-node.** Node heartbeats and recovery of nodes that never return, delivered with the PostgreSQL adapter.
6. **Consumers.** Notifications, then tasks and approval of publishing, as activities and built-in workflows.

## Tests

`internal/runner/runner_test.go` launches the real runner on temporary SQLite files with the PaperGo schema:

- an item trigger with a condition, a node that updates the item, and a second workflow started by the first one's `event.raise`; a rolled-back write logs no event and starts nothing; only workspace managers see runs;
- selection runs: one run over ordered members with a chosen primary item, `{run:items}` tokens, listing by any member, refusal of items from another collection; deleting a member cancels the run, unless the run deleted it itself;
- a process without a runner (another server) commits a change, and a runner launched later dispatches its event and runs the workflow;
- a condition that can no longer be evaluated produces a failed run with the reason and does not block later events;
- manual starts with typed inputs, defaults, variables and `if` branches; starting without access is refused;
- the `items.expire` built-in on a due schedule tick, unpublishing only expired published items; a second tick raises nothing; copying the built-in turns it off;
- a workflow that keeps updating its own item stops at depth 5;
- a run that fails because its author lost access, retried from the failed step after access returns;
- a real process crash in the middle of a run (a child test process exits while the run sleeps); relaunching with the same `NODE_ID` finishes the run without repeating the committed node.

`internal/workflow` unit tests cover definition validation, tokens and built-in parameters; `internal/httpapi/workflows_test.go` covers the routes. On PostgreSQL, the same suite will run with two nodes, including recovery of a node that never returns.

## Spike results

The spike ran DBOS v1.6.0 against the real PaperGo schema in temporary SQLite files, under `go test -race`. Its tests were replaced in phase 1 by the runner's own tests, which check the same properties on the real runner.

| Question | Test | Result |
| --- | --- | --- |
| Do DBOS tables coexist with the PaperGo schema? | `TestSpikeSchemaCoexistence` | Yes, in both orders (PaperGo first, or DBOS first). DBOS adds the 13 tables listed above, leaves PaperGo tables untouched, and `integrity_check` and `foreign_key_check` stay clean. |
| Does a rollback cancel the start? | `TestSpikeTransactionalEnqueue` | Yes. An Ent write and `Enqueue` share PaperGo's `*sql.Tx`: the run is invisible to other connections until commit, and neither the row nor the run exists after a rollback. Enqueuing a finished workflow ID again in another committed transaction keeps that transaction's write and returns the stored result without running the workflow again. |
| Does a rollback cancel a signal? | `TestSpikeTransactionalSignal` | Yes. A rolled-back `Send` never arrives (`Recv` ends with `ErrTimeout`). A committed one is delivered even though both sends commit while the workflow is held in an earlier step, before it reaches `Recv`. Sending twice with one idempotency key stores one message. |
| Are step writes exactly-once? | `TestSpikeExactlyOnceStep` | Yes, with the completion-marker design of exactly-once steps. The step fails after its transaction committed, DBOS retries it, and the retry returns the stored output without writing again. Two steps with the same name in one workflow both write. |
| Does work survive a process crash? | `TestSpikeCrashRecovery` | Yes. A child test process completes the first step and calls `os.Exit` inside the second. Relaunching with the same executor ID resumes the run at the second step; the first step's write stays single. A second crashed run, under an executor ID that never returns, completes on the live node after it lists the dead executor's pending runs and resumes them. |
| Does write contention break requests? | `TestSpikeWriteContention` | See the next table. |
| Does it build without CGO? | `CGO_ENABLED=0 go build ./cmd/api` and `CGO_ENABLED=0 go test -c ./internal/runner` | Yes. |

Contention: 6 goroutines create items through `dms.Service`, 6 others commit an Ent write plus an `Enqueue` each (120 of each), and DBOS runs the 120 workflows, each with a step that writes through a completion-marker step. Three runs each, without the race detector:

| PaperGo pool | Errors per run | Workflows completed |
| --- | --- | --- |
| deferred transactions, 5 s busy timeout (today's `database.Open`) | 99–139 (`database is locked`, `SQLITE_BUSY_SNAPSHOT`; steps out of retries) | 92–104 of 120 |
| `_txlock=immediate`, 5 s busy timeout | 0 | 120 |
| `_txlock=immediate`, 30 s busy timeout | 0 | 120 |

Each run takes about 2 seconds. Under the race detector, which slows everything about sixfold, the 5-second timeout still lost 2–3 enqueues to waits longer than 5 seconds; the 30-second timeout lost none. Hence `_txlock=immediate` with a 30-second busy timeout.

Other findings that shaped this plan:

- DBOS `Shutdown` closes the `*sql.DB` it was given, despite the comment saying the caller owns it, so DBOS gets its own handle.
- `RunAsTransaction` hands its callback a DBOS `Tx`, not a `*sql.Tx`, so Ent cannot run on it; exactly-once steps use a completion marker instead.
- `Send` to a workflow ID that does not exist fails on a foreign key, so signals go only to existing workflows.
- DBOS recovers only its own executor's work at launch; another executor's pending runs are taken over with `ListWorkflows` and `ResumeWorkflows`.
- DBOS start and stop take most of the runner tests' time.

## Dependencies

The runner's modules and licences are recorded in [`docs/dependencies.md`](../dependencies.md): DBOS Transact Go and `robfig/cron/v3` (MIT), and through DBOS the pgx family (MIT) and `gorilla/websocket` (BSD-2-Clause, unused Conductor client).
