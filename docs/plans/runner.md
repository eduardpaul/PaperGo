# Async runner (plan)

Status: **Proposed; phase 0 spike done** (see [Spike results](#spike-results)). The runner is not wired into the API yet. It is the foundation that notifications, tasks and workflow approval will build on; those plans are deferred until the runner is complete.

Reviewed against `main` at `f377d1f`. Engine facts were checked in the source of [DBOS Transact Go](https://github.com/dbos-inc/dbos-transact-golang) v1.6.0 (2026-10-07).

## Purpose

PaperGo has no asynchronous machinery. It has no outbox, worker, schedule or durable wait. Everything happens inside one request and one `dms.Service.write` transaction (`internal/dms/service.go:31`). The runner adds one model for everything that happens *after* something or *on a schedule*:

- reactions to domain events (an item was published, a task was completed);
- background jobs (long rebuilds, imports, extraction);
- cron schedules (retention, reconciliation, reminders);
- durable waits with timeouts (approvals, escalations);
- retries with backoff and visible failures.

It must keep PaperGo's minimal install (one process, one SQLite file) and also run unchanged on several nodes once PaperGo has a PostgreSQL adapter.

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

- `internal/runner` is the only package that imports DBOS. Domain code (`internal/dms`, `internal/httpapi`, `internal/webdav`) uses the PaperGo contract below, so the engine stays replaceable.
- Every API process is also a worker. A later `RUNNER_QUEUES` setting can split API and worker roles.
- **SQLite:** DBOS keeps its system tables in the **same database file** as PaperGo, so starting work is atomic with the domain write. This stays one node, as the [deployment rules](../../README.md#deployment-and-scaling) already require.
- **PostgreSQL** (with the future PostgreSQL adapter): the DBOS `dbos` schema goes in the same database. Any number of nodes share queues; DBOS dequeues with `SKIP LOCKED`.

## Database integration

The phase 0 spike proved this design; the numbers are in [Spike results](#spike-results).

- **Write transactions lock when they begin.** Today `database.Open` uses `database.DefaultOptions`: deferred transactions and a 5-second `busy_timeout`. Phase 1 changes the defaults to `_txlock=immediate` and 30 seconds. modernc keeps starting read-only transactions deferred, so reads keep their WAL snapshots. Write transactions take the write lock at `BEGIN` and wait their turn, instead of failing with `SQLITE_BUSY` when a deferred transaction tries to upgrade after DBOS has written. `writeMu` stays as the in-process queue for PaperGo writes.
- **DBOS has its own handle on the same file.** `internal/runner` opens a second `*sql.DB` with `database.DSN`, the helper `database.Open` uses, so both handles have the same pragmas, and passes it as `Config.SQLiteSystemDB`. DBOS `Shutdown` closes the handle it was given, so it must never be PaperGo's pool.
- **One transaction for Ent and the runner.** `write()` begins a `*sql.Tx` and builds the Ent client over it (`entsql.NewDriver(dialect.SQLite, entsql.Conn{ExecQuerier: tx})`). The same transaction carries domain rows, audit rows and runner calls (`Publish`, `Enqueue`, `Signal`); DBOS accepts the `*sql.Tx` from PaperGo's pool because both handles open the same database.
- **Reserved table names.** DBOS adds 13 tables without a prefix: `application_versions`, `dbos_migrations`, `event_dispatch_kv`, `notifications`, `operation_outputs`, `queues`, `streams`, `workflow_events`, `workflow_events_history`, `workflow_input`, `workflow_output`, `workflow_schedules`, `workflow_status`. PaperGo tables must not use these names; for example, a future inbox becomes `user_notifications`.
- **Migrations.** DBOS migrates its own tables, versioned in `dbos_migrations`. This is the one documented exception to the single Atlas file in [AGENTS.md](../../AGENTS.md): PaperGo never edits those tables. Phase 1 extends `CheckSchema` (`internal/database/database.go`) to check the DBOS migration version too. Atlas applies the PaperGo schema to a fresh file first; DBOS migrates its tables when the runner launches. Multi-node deployments run DBOS migrations once, before nodes start (`SkipMigrations` on the nodes).
- **Step completion markers.** Phase 1 adds one runner table to the schema file and `ent/schema`: `runner_step_results (workflow_id, step_id, step, output)`, for `runner.Tx` (below). The spike creates the same table in its test schema.

## PaperGo contract

| Call | Where | Meaning |
| --- | --- | --- |
| `t.Publish(ctx, Event{Type, WorkspaceID, ResourceID, Actor, Payload})` | inside `write()` | Starts one handler workflow per registered handler of `Type`, with workflow ID `evt:{eventID}:{handler}`. These are queued in the same transaction and cancelled by a rollback. There is no outbox table: the queued workflow is the durable message. |
| `t.Enqueue(ctx, job, input, Opts{Key, Delay, Queue, Partition, Priority})` | inside `write()` | Starts a job. `Key` becomes the workflow ID, so enqueuing the same key again returns the existing run. `Delay` schedules "run at" work, such as a reminder at a due date. |
| `t.Signal(ctx, workflowID, topic, payload, key)` | inside `write()` | Delivers a message to a workflow (`Send` with an idempotency key). The workflow must already exist: DBOS rejects a message to an unknown workflow ID. A message sent before the workflow reaches `Wait` is kept. |
| `runner.Wait(ctx, topic, timeout)` | workflow | Durable wait for a signal (`Recv`). A timeout returns `ErrTimeout`, the hook for escalation. |
| `runner.Sleep(ctx, d)` | workflow | Durable timer. |
| `runner.Tx(ctx, step, func(t *dms.Service) error)` | workflow step | Writes PaperGo data **exactly once**, through the same invariants, audit and `Publish` as a request. The step's writes and a `runner_step_results` row keyed by workflow ID and DBOS step ID commit in one transaction; a retried or recovered step finds the row and returns the stored output. Replay assigns step IDs in the same order, so one step name can be used for several writes, such as a loop over recipients; the recorded name must match on replay. The output is always returned decoded from the row, so a first run and a replay see the same value. DBOS `RunAsTransaction` cannot be used: its callback gets a DBOS `Tx` without the `*sql.Tx`, so Ent cannot run on it. |
| `runner.Schedule{Name, Cron, Job, Queue}` | registration | Cron schedules declared in code. Startup creates new schedules, updates changed ones and removes ones no longer declared. |

Rules:

- **Event types are a contract.** Each type documents its payload. Events are separate from `audit_events`: audit is the manager's record of what changed; events are what the runner reacts to.
- **Steps carry the actor.** A job started by a user records that subject and re-checks authorization in `runner.Tx`. Only system-owned work (retention, reconciliation) runs as the reserved subject `system:runner`, and that subject can never be granted access.
- **Queues:**
  - `events`: handlers, bounded concurrency.
  - `maintenance`: concurrency 1.
  - `bulk`: partitioned by collection ID, so one collection's rebuilds run in order while different collections run in parallel.

## Semantics

- **Delivery.** Ordinary steps run at least once and must be idempotent. `runner.Tx` steps are exactly once. Starts and signals are atomic with the transaction that calls them.
- **Failures.** Steps retry with exponential backoff (`WithStepMaxRetries`, `WithStepBackoffFactor`). An error after retries ends the workflow in `ERROR`. A workflow that keeps crashing its process exceeds its maximum recovery attempts and is parked. Both are visible through the admin API and can be resumed or cancelled.
- **Workflow code is replayed.** After a crash, DBOS reruns the workflow function and returns recorded step results. Review checklist for workflow functions (steps are exempt):
  - no `time.Now`, randomness, map-order dependence or I/O outside steps;
  - no reads of mutable globals;
  - change running logic only behind `runner.Patch` (DBOS `Patch`/`DeprecatePatch`);
  - keep inputs and outputs JSON-serializable and small.
- **Versions.** By default DBOS stamps each workflow with a hash of the binary and recovers only workflows from the same binary, so a deploy would strand pending work. PaperGo enables patching (`EnablePatching`), which fixes the application version, and uses `Patch` for incompatible changes.

## Multi-node

On PostgreSQL several nodes share queues and schedules safely. Two pieces are PaperGo's responsibility:

- **Node identity.** Each node has a stable `NODE_ID`, used as the DBOS executor ID. A restarted node recovers its own pending workflows.
- **Recovering a node that never returns.** Nodes heartbeat into `runner_nodes`. The holder of a `runner_leases` row takes over the pending workflows of a node whose heartbeat expired: it lists them (`ListWorkflows` filtered by that executor ID and `PENDING`) and calls `ResumeWorkflows`, which puts them back on the internal queue, where the live nodes run them. A node that was only slow may still be running one of them; `runner.Tx` markers keep its writes single, and ordinary steps are idempotent anyway.

Other in-process state also needs shared implementations before a second node: WebDAV locks (`internal/webdav/locks.go`), and later the live SSE hub (PostgreSQL `LISTEN/NOTIFY`).

## Review of `main`: what the runner serves today

Existing behaviour that the runner can take over or complete:

| Area | Today on `main` | With the runner |
| --- | --- | --- |
| Index and business-key rebuilds | `UpdateField` (`internal/dms/schema.go:120-129`) and `ApplyTemplate` (`internal/dms/templates.go:252-256`) call `rebuildSurfaces`/`rebuildBusinessKeys` in pages of 500 (`surfaceBatch`, `internal/dms/schema.go:199`), all inside the request's write transaction. A large collection holds the single writer for the whole rebuild. | A `bulk` job, partitioned by collection, of resumable batch steps in `runner.Tx`. This needs a defined consistency contract while a rebuild is pending, for example the collection reports `index_pending` and indexed queries over the affected field are rejected until it finishes, as [the architecture](../architecture.md) requires for any asynchronous projection. |
| Content-type validation of existing heads | `checkTypeHeads` (`internal/dms/contenttypes.go:226`) scans every item of a type in the request. | Stays synchronous while it is a precondition of the change. A job is only worth it if validation becomes advisory. |
| Switching a collection to automatic publishing | `publishAllHeads` (`internal/dms/revisions.go:357`), called from `internal/dms/service.go:378`, publishes every unpublished head in one transaction. | The same partitioned `bulk` job pattern. |
| Orphaned blobs | A crash between writing the object and committing metadata leaves an unreferenced file. Ordinary failures clean up best-effort (`internal/httpapi/api.go:493`, `internal/webdav/methods.go:92`). The README asks deployments to reconcile orphans after a grace period. | A `maintenance` schedule that compares stored objects with `blobs` and deletes unreferenced objects older than the grace period. |
| Expired WebDAV app passwords | Rejected at use (`internal/dms/credentials.go:95`) but never removed. | A `maintenance` schedule purges expired credentials. |
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

- **Admin API:**
  - `GET /v1/admin/runs?status=&name=&after=` lists runs (`ListWorkflows`).
  - `GET /v1/admin/runs/{id}` shows a run and its steps (`GetWorkflowSteps`).
  - `POST /v1/admin/runs/{id}/cancel` and `/resume` act on a run.
  - PaperGo has no deployment-level role, so a new `ADMIN_SUBJECTS` setting lists the allowed subjects.
- **Retention:** a `maintenance` schedule deletes runs that finished more than `RUNNER_RETENTION` ago (default 30 days). It first deletes their `runner_step_results` rows, then the runs (`DeleteWorkflows`). In that order, a crash in between leaves only a finished run without markers, which is never replayed; markers never outlive their run, so a later run that reuses a `Key` starts without stale markers.
- **Configuration:**

  | Setting | Meaning |
  | --- | --- |
  | `RUNNER_ENABLED` | Default `true` |
  | `NODE_ID` | Default `local`; required and unique on PostgreSQL |
  | `RUNNER_RETENTION` | How long finished runs are kept |
  | `ADMIN_SUBJECTS` | Subjects allowed to use the admin API |

- **Lifecycle:** `cmd/api/main.go` registers handlers, jobs and schedules, calls `Launch` before serving, and calls `Shutdown` inside the existing 15-second graceful stop. Readiness fails when the runner has stopped.
- **Observability:** structured `slog` logs per run and step. Metrics and tracing export follow the general observability work.

## Phases

1. **Spike** (done). See [Spike results](#spike-results).
2. **Core.** The contract above, retention, the admin read API, and the first users: the credential purge and orphan reconciliation schedules.
3. **Long work.** Move index and business-key rebuilds and `publishAllHeads` to partitioned jobs, with the pending-index contract; admin cancel and resume.
4. **Multi-node.** Node heartbeats and recovery of nodes that never return, delivered with the PostgreSQL adapter.
5. **Consumers.** Notifications, then tasks and approval of publishing.

## Tests

Tests use temporary SQLite databases, as `internal/testutil` does today, plus a helper that launches the runner and waits for a run's result:

- A rollback after `Publish` or `Enqueue` leaves no run.
- A repeated `Key` returns the existing run.
- A `Signal` sent before `Wait` is still received.
- `runner.Tx` effects happen once across a real process crash and relaunch with the same `NODE_ID`.
- Schedules are created, changed and removed from code.
- Retention deletes only finished runs, together with their step markers.
- Concurrent request writes and runner writes never fail with `SQLITE_BUSY`.

On PostgreSQL, the same suite runs with two nodes, including recovery of a node that never returns.

## Spike results

The spike is `internal/runner/spike_test.go`. It runs DBOS v1.6.0 against the real PaperGo schema in temporary SQLite files and passes under `go test -race`.

| Question | Test | Result |
| --- | --- | --- |
| Do DBOS tables coexist with the PaperGo schema? | `TestSpikeSchemaCoexistence` | Yes, in both orders (PaperGo first, or DBOS first). DBOS adds the 13 tables listed above, leaves PaperGo tables untouched, and `integrity_check` and `foreign_key_check` stay clean. |
| Does a rollback cancel the start? | `TestSpikeTransactionalEnqueue` | Yes. An Ent write and `Enqueue` share PaperGo's `*sql.Tx`: the run is invisible to other connections until commit, and neither the row nor the run exists after a rollback. Enqueuing a finished workflow ID again in another committed transaction keeps that transaction's write and returns the stored result without running the workflow again. |
| Does a rollback cancel a signal? | `TestSpikeTransactionalSignal` | Yes. A rolled-back `Send` never arrives (`Recv` ends with `ErrTimeout`). A committed one is delivered even though both sends commit while the workflow is held in an earlier step, before it reaches `Recv`. Sending twice with one idempotency key stores one message. |
| Are step writes exactly-once? | `TestSpikeExactlyOnceStep` | Yes, with the completion-marker design of `runner.Tx`. The step fails after its transaction committed, DBOS retries it, and the retry returns the stored output without writing again. Two steps with the same name in one workflow both write. |
| Does work survive a process crash? | `TestSpikeCrashRecovery` | Yes. A child test process completes the first step and calls `os.Exit` inside the second. Relaunching with the same executor ID resumes the run at the second step; the first step's write stays single. A second crashed run, under an executor ID that never returns, completes on the live node after it lists the dead executor's pending runs and resumes them. |
| Does write contention break requests? | `TestSpikeWriteContention` | See the next table. |
| Does it build without CGO? | `CGO_ENABLED=0 go build ./cmd/api` and `CGO_ENABLED=0 go test -c ./internal/runner` | Yes. |

Contention: 6 goroutines create items through `dms.Service`, 6 others commit an Ent write plus an `Enqueue` each (120 of each), and DBOS runs the 120 workflows, each with a step that writes through `runner.Tx`. Three runs each, without the race detector:

| PaperGo pool | Errors per run | Workflows completed |
| --- | --- | --- |
| deferred transactions, 5 s busy timeout (today's `database.Open`) | 99–139 (`database is locked`, `SQLITE_BUSY_SNAPSHOT`; steps out of retries) | 92–104 of 120 |
| `_txlock=immediate`, 5 s busy timeout | 0 | 120 |
| `_txlock=immediate`, 30 s busy timeout | 0 | 120 |

Each run takes about 2 seconds. Under the race detector, which slows everything about sixfold, the 5-second timeout still lost 2–3 enqueues to waits longer than 5 seconds; the 30-second timeout lost none. Hence `_txlock=immediate` with a 30-second busy timeout.

Other findings that shaped this plan:

- DBOS `Shutdown` closes the `*sql.DB` it was given, despite the comment saying the caller owns it, so DBOS gets its own handle.
- `RunAsTransaction` hands its callback a DBOS `Tx`, not a `*sql.Tx`, so Ent cannot run on it; `runner.Tx` uses a completion marker instead.
- `Send` to a workflow ID that does not exist fails on a foreign key, so signals go only to existing workflows.
- DBOS recovers only its own executor's work at launch; another executor's pending runs are taken over with `ListWorkflows` and `ResumeWorkflows`.
- The spike tests take about 80 seconds under `-race`, mostly DBOS start and stop; phase 1 tests should share one launched runner per package.

## Dependencies

| Module | Licence |
| --- | --- |
| `github.com/dbos-inc/dbos-transact-golang` v1.6.0 | MIT |
| `github.com/jackc/pgx/v5`, `puddle/v2`, `pgpassfile`, `pgservicefile`, `pgerrcode` | MIT |
| `github.com/robfig/cron/v3` | MIT |
| `github.com/gorilla/websocket` (DBOS Conductor client, unused) | BSD-2-Clause |

`modernc.org/sqlite` and `github.com/google/uuid` are already used. Adopting the runner adds a `docs/dependencies.md` register recording each dependency and its licence.
