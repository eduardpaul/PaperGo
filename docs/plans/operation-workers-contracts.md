# Operation workers: Phase 0 contracts

Status: **proposal for review**. This is the Phase 0 deliverable of [the operation workers plan](operation-workers.md). It inventories `main` at `15733bc` and covers:

- the Core sockets and the execution state machine;
- the storage changes and why they are needed;
- submission identity, quotas and fairness, and the six clocks;
- worker configuration, controls, residency and artifacts;
- acceptance tests mapped to phases.

Decisions D1–D9 are [settled](#settled-decisions); Phase 1 can start. CPU and memory budgets are out of scope (see the plan's deferred work).

## 1. Inventory

### Operation kinds

| Kind | Step | Scope | Producers | Terminal side effects |
| --- | --- | --- | --- | --- |
| `field_index` | transactional | none (acts for no one) | `enqueueFieldIndex` (`internal/dms/operations.go:24`) on field indexing changes | `Failed` sets the field `index_failed`; success sets it ready when no other operation is pending |
| `bulk_update` | transactional | `list.write` | `StartBulkUpdate` ← `POST /v1/resources/{id}/bulk-update` | `Failed` clears `operation_items` |
| `search_reindex` | transactional | `organization.manage` | `POST /v1/search/reindex`; `RebuildSearch` at startup as the `papergo` system subject; portability import | Clears the in-memory `searchRebuild` flag |
| `integration.refresh` | **external** | `list.write` | Integration create/update/refresh routes; scheduled `integrations.refresh` built-in workflow | `Failed` writes the integration error. Has its own integration-row lease (`ErrIntegrationBusy`) |
| `portability.export` / `portability.import` | transactional | `organization.manage` | `RegisterPortability` ← `POST /v1/portability/{exports,imports}` | `ObjectReferenced` keeps import objects while pending and export objects while their result is unexpired |

Start routes answer `202` with `Location: /v1/operations/{id}`. Results are read from `GET /v1/operations/{id}` and `GET /v1/resources/{id}/operations`; `GET /v1/events` also streams `operation` frames.

### Execution today

- `RunOperations` (`internal/dms/operations.go:66`) is one goroutine. It always selects the **oldest** pending operation, wakes on a size-1 channel or once a minute, and runs operation, delta and live-change retention every hour.
- There is no execution claim. Exclusivity comes only from the single process and the serial loop.
- **Head-of-line blocking:** `ErrIntegrationBusy` makes the loop report "no work". The next pass selects the same oldest operation, so all other kinds wait until that integration's lease clears. A long external step blocks every kind the same way.
- Authorization is rechecked before every step, except for `field_index` and the system `search_reindex`.
- Every error and every panic is terminal. There is no retry classification.
- `Supersede` writes no live notification, unlike `Enqueue` and `Save`.
- `cmd/api/main.go:277-278` starts `RunOperations` and `RunBlobCleanup` without waiting for them on shutdown.

### Workflow callers and their target owner

| Caller | Engine | Today | Target |
| --- | --- | --- | --- |
| `document.read_text` | PDFium + OCR | DBOS step behind `workGate`; result in `blob_text` keyed by sha256 + settings | Phase 3: consumer of `document.text` |
| `file.optimize_image` | OCR | Stages a WebP for the run; ports `candidate`/`skipped` | Phase 3: consumer of `document.optimize_image` |
| `file.compose_pdf` | OCR | Stages a PDF for the primary item from ordered photos | Phase 3: consumer of `document.compose_pdf` |
| `file.promote` | — | Promotes a staged file *of this run* (`runStaged`, `internal/dms/staged.go:215`) | Unchanged: the run stages a consumed operation's output itself, then promotes it (§4) |
| `ai.complete` (batch), `search.enrich` | Remote | Workflow waits, consumed by the `ai.batch` coordinator workflow | Phase 4: consumers of AI operations |
| `ai.decide`, `taxonomy.decide` | Laya | DBOS step behind `workGate`; workspace-scoped result cache (`Results.Get(namespace, workspace, key)`) | Phase 5: consumers of decision operations |
| `search.embed` | Embeddings | Immediate, or left to the sweep | Phase 6: consumer of the embedding module |
| Search query (`dms.embedSearch`) | Embeddings | Synchronous; falls back to keyword on error | Phase 3: resource handle. Phase 6: latency/capacity policy (never an operation) |

### Resources

| Engine | Gate | Idle eviction | Close |
| --- | --- | --- | --- |
| PaddleOCR | **Package-global** (`internal/ocr/paddle/paddle.go:83`) | Hard-coded 5 min | Unloads; reloads on next use |
| Local embeddings | Per instance | Hard-coded 5 min | Permanent |
| Laya | Per instance | Hard-coded 5 min | Permanent |
| PDFium | Process-wide single-threaded pool | None | None |

`cmd/api` builds one instance of each engine and shares it. `workGate` (`internal/workflow/tools.go:83`) adds a second process-wide gate around OCR, optimization, composition and decisions, but not embeddings.

### Schedules and maintenance

| Schedule | Owner today | Work | Target owner |
| --- | --- | --- | --- |
| `papergo.tick` (DBOS, every minute) | Runner | Raises workflow schedule triggers, including `integrations.refresh` and `ai.batch` | Stays for workflow triggers. `ai.batch` is removed in Phase 4. `integrations.refresh` stays workflow orchestration that enqueues operations |
| `papergo.purge` (DBOS, hourly at :45) | Runner | Run retention, `activitystore.Purge`, `resultcache.PurgeExpired`, dispatched events | Activity purge and result cache expiry move to worker maintenance in Phase 2. Run and event retention stay with the runner |
| `papergo.purge_system` (DBOS, hourly at :15) | Runner | DBOS system tables | Stays |
| `papergo.search_embed` (DBOS, every 15 min) | Runner | Embedding sweep, at most 2000 inputs | Phase 6: worker maintenance |
| `RunOperations` hourly | dms loop | Operation, delta and live-change retention | Phase 2: worker maintenance |
| `RunBlobCleanup` every minute | Goroutine | Purged blobs, orphan objects (1 h grace), reclaim sweep | Phase 2: worker maintenance |

### Retention and ownership primitives

- `blob_leases(owner_id, run_id NOT NULL → workflow_runs, blob_id)`. All callers are workflow code.
- `staged_files(run_id NOT NULL → workflow_runs, …, state pending|promoted|discarded)`. Promotion and discard require the same run. Run retention discards pending staged files (`DiscardRunStagedFiles`). `ObjectReferenced` keeps objects of `pending` staged files.
- `activity_references(owner_id, record_id)`. `Purge` drops references owned by a terminal `workflow_runs` id (`store.go:241`) or by an expired record.
- `activity_records` leases: `Claim` lets a holder take over from a terminal `workflow_runs` holder (`store.go:176`).
- Operations are deleted 30 days after a terminal update.

### Provider retries

`internal/ai/http.go:20-25` makes four attempts and sleeps in-process for up to 30 s per `Retry-After`. The batch client uses the same code. Under a worker, this loop would multiply the operation's attempts and hold the slot while it sleeps.

### Other findings

- `contentHash` (`internal/workflowai/cache.go:279`) takes `queue` but does not hash it.
- The flight status `cleared` is checked (`coordinate.go:889`) but never written.
- No test compares registered routes with `api/openapi.json`.

## 2. Submission identity and execution ownership (socket 1)

### Why the claim lives on `operations` (D1, settled)

Activity records cannot provide the following without becoming a second queue:

1. An atomic select-and-claim on kind, status and due time.
2. An indexed due-time lookup.
3. One record per operation (keeping a separate claim record would duplicate every pending operation).
4. A fence on the same row that the result updates.

Activity records stay the home of **module** state: provider flights, drain markers, cron cursors, worker controls and status.

### Schema change to `operations` (both engines)

| Column | Type | Purpose |
| --- | --- | --- |
| `workspace_id` | `TEXT REFERENCES resources(id) ON DELETE CASCADE` | Workspace attribution for quotas, fairness and cache scope. `NULL` means organization work. Set at acceptance from the container, or from the explicit workspace of inline requests. |
| `attempt_at` | `DATETIME NOT NULL` | Earliest claim time. `created_at` at enqueue; later the retry-not-before time. |
| `attempts` | `INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0)` | Claims that ended without a terminal state. |
| `claim_holder`, `claim_token`, `claim_until` | `TEXT`, `TEXT`, `DATETIME` | The execution lease. A new token on every claim. |
| `execution_id` | `TEXT NOT NULL DEFAULT ''` | Membership in a durable module execution (a provider batch). Excludes the operation from claims. |
| `group_key`, `input_key` | `TEXT NOT NULL DEFAULT '' CHECK (length(...) <= 128)` | Feature-defined compatibility group and deduplication identity. |
| `deadline` | `DATETIME` | Completion deadline. `NULL` means none. |
| `idempotency_key` | `TEXT NOT NULL DEFAULT '' CHECK (length(idempotency_key) <= 128)` | Caller's key. Empty means none (D5). |
| `payload_digest` | `TEXT NOT NULL DEFAULT ''` | SHA-256 of canonical `{kind, container, payload}`. |
| `error_code` | `TEXT NOT NULL DEFAULT '' CHECK (error_code IN ('', 'deadline_exceeded', 'retries_exhausted', 'forbidden'))` | Machine-readable failure reason (D7). |

`status` gains `cancelled` (D2, settled). Go types: `operations.Status` gains `StatusCancelled`, and a new `operations.ErrorCode` type has its constants and `Valid()`.

```sql
CREATE INDEX operation_ready ON operations(kind, workspace_id, attempt_at) WHERE status = 'pending';
CREATE INDEX operation_group ON operations(kind, group_key, created_at) WHERE status = 'pending';
CREATE INDEX operation_quota_workspace ON operations(workspace_id) WHERE status = 'pending';
CREATE INDEX operation_quota_creator ON operations(created_by) WHERE status = 'pending';
CREATE UNIQUE INDEX operation_idempotency ON operations(created_by, kind, COALESCE(container_id, ''), idempotency_key) WHERE idempotency_key <> '';
```

Both engines support partial and expression indexes. `COALESCE` is required because `NULL` containers never conflict in a unique index.

### State machine

The public status is `pending | succeeded | failed | superseded | cancelled`. The execution state of a pending operation is derived from the new columns:

```mermaid
stateDiagram-v2
    [*] --> Ready: Enqueue (attempt_at = created_at)
    Ready --> Claimed: Claim (token T, attempts+1)
    Deferred --> Ready: now >= attempt_at
    Claimed --> Claimed: Renew(T) / Checkpoint(T)
    Claimed --> Ready: Release(T) (shutdown, drain, yield; attempts-1) / claim expires
    Claimed --> Deferred: Defer(T, at, cause)
    Claimed --> Parked: Park(T, E)
    Parked --> Ready: Unpark(E)
    Parked --> Deferred: Unpark(E, at)
    Claimed --> Terminal: Finish(T)
    Parked --> Terminal: FinishMember(E)
    Ready --> Terminal: Supersede / Cancel / deadline
    Deferred --> Terminal: Supersede / Cancel / deadline
    Claimed --> Terminal: Supersede / Cancel / deadline (T invalidated)
    Parked --> Terminal: Supersede / Cancel / deadline (member skipped by E)
    Terminal --> [*]: retention (30 days)
```

| Derived state | Condition (pending) | `waiting` in the view |
| --- | --- | --- |
| Ready | no live claim, `execution_id = ''`, `attempt_at <= now` | `queued`; worker status adds `admission`, `window`, `capacity`, `paused` or `quota` |
| Deferred | no live claim, `attempt_at > now` | `retry` when `attempts > 0`, otherwise `scheduled` |
| Claimed | `claim_until > now` | `running` |
| Parked | `execution_id != ''` | `remote` |

`OperationView` gains these fields:

- `waiting`, computed when read and never stored;
- `attempts`;
- `next_attempt_at`;
- `deadline`;
- `error_code`;
- `workspace_id`.

### Invariants

1. **Fenced writes only.** Any write to a pending operation requires `claim_token = T AND claim_until > now`, or `execution_id = E` for a parked member. The exceptions are `Enqueue`, `Supersede`, `Cancel`, `Expire` and `Unpark`. `Save` and `Next` are deleted.
2. **Terminal transitions clear ownership.** `Finish`, `Supersede`, `Cancel` and `Expire` clear the claim and `execution_id`. A stale executor's next fenced write affects no rows and returns `operations.ErrClaimLost`.
3. **Terminal transitions release, in the same transaction:**
   - operation-owned blob leases;
   - operation-owned activity references.

   They also write a live change (`Supersede` now does too).
4. **Expiry is recovery.** An expired claim is ready again, with no startup sweep. A claimer that sees `attempts >= max_attempts` fails the operation with `retries_exhausted` instead of executing it.
5. **Deadlines.** `Expire(now)` fails pending operations with `deadline < now` with code `deadline_exceeded`. This includes parked members; their batch continues for the others. It runs as part of every worker pass for that worker's kinds, and as maintenance. A fenced `Finish` with `succeeded` after the deadline is refused.
6. **Clock.** Callers pass `now` (the runtime's injected clock), in UTC and truncated to microseconds.

### Idempotent acceptance

- **Header.** `Idempotency-Key` is 1–128 printable ASCII characters and is accepted by every operation-producing route. `dms.EnqueueOperation` and the internal enqueue paths take it from the request context, so Core and module routes behave the same.
- **Lookup.** In the acceptance transaction, after `operationPermission` (current authorization always applies):
  - find a pending or retained operation by `(created_by, kind, container, key)`;
  - if none exists, insert one;
  - if one exists with the same `payload_digest`, return it with `200` and `Idempotent-Replayed: true` instead of `202`;
  - if the digest differs, return `409` with code `idempotency_conflict`.
- **Concurrency.** Two identical requests race to the unique index. The loser's insert fails, and the transaction retries once and reads the winner.
- **Lifetime.** A binding lives as long as its operation (pending, then 30 days after it finishes). After retention, the same key creates a new operation. The OpenAPI description of the header states this.
- **Scope.** The key is scoped per subject, not per access token. A caller never sees another subject's operation, because the key lookup includes `created_by`.

### Quotas

The `operations` configuration section sets the limits. Defaults:

```yaml
operations:
  quotas:
    per_creator: 1000
    per_workspace: 5000
    installation: 50000
    payload_bytes_per_workspace: 256MiB
```

- They are checked in the acceptance transaction using the quota indexes.
- System-created operations (`field_index`, system `search_reindex`) are exempt, and so is a replayed idempotent request.
- Excess is rejected with `429`, code `operation_quota`, and `Retry-After: 60`, and nothing is persisted.
- On PostgreSQL the count can overshoot by concurrent acceptances under `SERIALIZABLE` retries at most once per conflicting transaction. That bound is accepted.

### Fair dispatch

Each worker keeps, **in memory**, the last time it served each workspace. A pass claims one operation per workspace in least-recently-served order (`ReadyWorkspaces`, then `Claim{WorkspaceID}`), until capacity is full.

- **Aging:** an operation that has been ready for longer than `starvation_after` (default 10 m) is claimed first, oldest first.
- **Priority:** a worker can give its kinds bounded priorities (0–3). Higher priority is served first within the same aging tier.
- **Groups:** group policies choose *which group* is admitted fairly across workspaces and groups. They never reorder a group's members or a frozen drain, which stay oldest first.
- Fairness needs no durable state. After a restart it starts even.

### DAL signatures

```go
// internal/dal/operations
var ErrClaimLost = errors.New("operation claim was lost")
var ErrIdempotencyConflict = errors.New("idempotency key was used with another payload")

type Claim struct{ Holder, Token string; Until time.Time }

type ClaimRequest struct {
    Kinds       []string
    WorkspaceID *string    // nil: any; "": organization work
    GroupKey    *string
    Through     *time.Time // drain cutoff on created_at
    Holder      string
    TTL         time.Duration
    Now         time.Time
    Limit       int        // operations, or distinct inputs with GroupKey
    MaxAttempts int
}

type Group struct {
    Kind, Key   string
    Inputs      int
    Oldest      time.Time
}

type Quota struct {
    Creator, Workspace, Installation int64
    WorkspacePayloadBytes            int64
}

func (r *Repo) Accept(ctx context.Context, in Operation) (op Operation, replayed bool, err error)
func (r *Repo) PendingCounts(ctx context.Context, creator, workspaceID string) (Quota, error)
func (r *Repo) Claim(ctx context.Context, in ClaimRequest) ([]Operation, error)
func (r *Repo) ReadyWorkspaces(ctx context.Context, kinds []string, now time.Time, limit int) ([]string, error)
func (r *Repo) ReadyGroups(ctx context.Context, kinds []string, now time.Time) ([]Group, error)
func (r *Repo) NextAttempt(ctx context.Context, kinds []string, now time.Time) (time.Time, error)
func (r *Repo) Renew(ctx context.Context, id string, c Claim, until time.Time) error
func (r *Repo) Release(ctx context.Context, id string, c Claim) error
func (r *Repo) Defer(ctx context.Context, id string, c Claim, at time.Time, cause string) error
func (r *Repo) Checkpoint(ctx context.Context, op *Operation, c Claim) error
func (r *Repo) Finish(ctx context.Context, op *Operation, c Claim) error
func (r *Repo) Park(ctx context.Context, ids []string, c Claim, executionID string) error
func (r *Repo) Unpark(ctx context.Context, executionID string, ids []string, at time.Time) error
func (r *Repo) FinishMember(ctx context.Context, op *Operation, executionID string) error
func (r *Repo) Cancel(ctx context.Context, id string, now time.Time) error
func (r *Repo) Expire(ctx context.Context, kinds []string, now time.Time, limit int) (int, error)
func (r *Repo) Finished(ctx context.Context, ids []string) ([]Operation, error)
```

The claim is a single statement: `UPDATE … WHERE id IN (SELECT … LIMIT n) RETURNING …`. On SQLite the writer slot makes it exclusive. On PostgreSQL the subquery uses `FOR UPDATE SKIP LOCKED`. `Enqueue` becomes `Accept`.

### Service contract

```go
type OperationHandler struct {
    Scope     string
    Validate  func(context.Context, *Service, string, string, json.RawMessage) error
    // Admission returns the group and input identity of an accepted payload.
    Admission func(json.RawMessage) (group, input string, err error)
    // Deadline is the default completion deadline; zero means none.
    Deadline  time.Duration
    // Step runs one bounded batch inside the claim's write transaction.
    Step      func(context.Context, *Service, *operations.Operation) error
    // Execute runs outside the writer and applies results under its claim.
    Execute   func(context.Context, *Service, *Execution) error
    Failed    func(context.Context, *Service, *operations.Operation, error) error
}

func Retry(err error, after time.Duration) error // transient; after <= 0 uses backoff
```

- `ExternalStep` is replaced by `Execute`. `integration.refresh` returns `Retry(ErrIntegrationBusy, leaseRemaining)`.
- Unmarked errors and panics stay terminal. `dms.ErrForbidden` from a recheck fails with code `forbidden`.
- **Transactional steps** claim, step, and finish or release within one write transaction, with `BackgroundWrite` priority.
- **External executions** claim in a short transaction, renew at a third of the TTL, and apply under a fenced `Finish`. A failed renewal cancels the context.

### Authority and freshness

| Boundary | Check |
| --- | --- |
| Acceptance | `operationPermission` + `Validate` + quota + idempotency, then source leases, in one transaction |
| Claim / step | `operationContext` + `operationPermission`, as today, with the two system exceptions |
| Batch admission | Each member is rechecked; a revoked member fails alone with `forbidden` |
| Result application | The fenced `Finish` transaction rechecks permission and the feature's freshness guards (source currency, taxonomy fingerprint) |

## 3. The six clocks

| Clock | Stored as | Set by | On expiry |
| --- | --- | --- | --- |
| Maximum admission wait | config + `created_at` | worker `admission.max_wait` | the group becomes eligible; the reason becomes `window`/`capacity` if still blocked |
| Completion deadline | `operations.deadline` | the caller (bounded by the kind's maximum) or `OperationHandler.Deadline`; `AI_BATCH_DEADLINE` for AI kinds | `Expire` → `failed` / `deadline_exceeded` |
| Execution timeout | config | worker `execution_timeout` (default 10 m) | context cancelled → `Retry` (counts an attempt) |
| Retry budget | `attempts` + `started_at` | worker `retry.max_attempts`, `retry.max_elapsed`, backoff | `failed` / `retries_exhausted` |
| Execution lease | `claim_until` | worker `claim.ttl` (default 2 m), renewed at ⅓ | claim void; operation ready again |
| Inactivity timeout | in memory (last release) | resource `idle_unload` | resource unloaded when not held |

- A closed window holds overdue work. The status reports `window` with the next opening.
- `attempt_at` stores every retry-not-before time, so an overdue admission deadline cannot cause a hot loop.
- **Retry ownership.** `internal/ai` clients gain `Attempts` (default 1 under a worker) and return `ai.RetryAfter{After}` as an error. The worker maps it to `Retry(err, after)`. Short in-process jitter (≤ 2 s) is allowed only within one attempt.

## 4. Resource ownership (socket 3)

### Typed owners (D4, settled)

**`blob_leases`.** `run_id` becomes nullable, and the table gains `operation_id TEXT REFERENCES operations(id) ON DELETE CASCADE` and `CHECK ((run_id IS NULL) <> (operation_id IS NULL))`, plus an index on `operation_id`. Reclamation only asks whether a lease exists, so it does not change. `staged_files` keeps its required `run_id` (D8).

**`activity_references`.** It gains `owner_kind TEXT NOT NULL CHECK (owner_kind IN ('run', 'wait', 'record', 'operation'))` (Go type `activitystore.OwnerKind`). The unique key becomes `(owner_kind, owner_id, record_id)`. `Purge` gets one explicit rule per kind:

| Owner kind | Dropped when |
| --- | --- |
| `run` | the run is terminal or deleted |
| `wait` | the wait is closed or deleted |
| `record` | the owning record expired |
| `operation` | the operation is terminal (released in its terminal transaction; Purge is the backstop) |

**`activity_records` leases.** `Claim` loses its `workflow_runs` takeover clause. Expiry is the only takeover rule, and terminal runs already release their leases explicitly.

### Source bytes

A source lease is taken at acceptance and released at the terminal transition. Results that outlive the source are stored separately; `blob_text` is keyed by content hash.

### Generated outputs (D8)

Staging stays workflow-only: `staged_files` keeps `run_id NOT NULL`, its states and its run-scoped promotion and discard. Operations produce outputs; only a workflow run stages them.

1. The executor computes the output (in memory or a temporary file), uploads it as an object, and immediately applies the fenced `Finish`. The result records `object_key`, size, SHA-256, the sources it was made from, and the feature's outcome (for example `candidate` or `skipped`).
2. `ObjectReferenced` generalizes its portability-specific operation check. A kind registered as producing objects keeps its result's `object_key` referenced while the succeeded operation is retained; `portability.export` keeps its `expires_at` rule and `portability.import` its pending-payload rule. `blobs.Packages` becomes this registered set.
3. Between upload and commit there is one transaction, so the one-hour orphan grace period covers it however long the computation took. A lost claim or a crash in that gap leaves an unreferenced object that the orphan sweep collects. Failed and stale attempts never publish an output.
4. **Workflow consumer.** When the run's operation wait resolves, the resolving transaction stages the output for the run with the ordinary `StageFile` (write access and source currency are rechecked there; a changed source follows `skipped`). From then on the staged row references the object as it does today, and `file.promote` and run retention are unchanged.
5. **API caller.** `document.optimize_image` and `document.compose_pdf` have no public routes in this plan: an API caller could not stage or promote their output. Their operations are submitted by workflow actions only. Document text keeps its public route.
6. Cancelling, superseding or expiring an operation before it succeeds publishes nothing. Cancelling a workflow consumer after the output was staged follows run cancellation, as today.

Optimization and composition become operations even though only workflows consume them today, because the workflow engine is replaced in the next release. The operation output is the engine-independent temporary storage. Staging into `staged_files` is the current engine's consumer adapter: it is deleted together with the JSON interpreter, and the replacement engine consumes the operation output directly. Its review and promotion path from an operation output is designed on the script branch, not here.

## 5. Completion observation (socket 2)

- **Durable state is the truth.** A consumer records the operation id in its own state, then reads it with `Finished(ids)`, so an operation that finished before the consumer subscribed needs no special case.
- **Wake-up.** `dms.Service.OnOperationsFinished(func(ids []string))` runs after commit for transactions that moved operations to a terminal state. `cmd/api` wires it to `Runner.Wake`. Live changes keep serving SSE clients.
- **Workflow consumer.** `internal/workflow` registers a wait resolver of kind `operation` (key = operation id) that uses `Finished`, stages generated outputs for the run (§4), and completes the wait with `{status, error_code, result}`. Core never writes `workflow_waits`.
- Operations raise no domain events.

## 6. Module routes (socket 4)

```go
// internal/httpapi
type Module interface {
    Name() string
    Routes() []Route
}

type Route struct {
    Method, Pattern string
    OperationID     string
    Write           bool
    Handle          func(http.ResponseWriter, *http.Request, Request) error
}

type Request struct {
    Subject string
    DMS     *dms.Service
}
```

- `API.Modules []Module` is registered behind `authenticate` and `limiter`. Errors go through `failure()`.
- `Decode`, `Accepted` (202 or replayed 200, `Location`) and `JSON` are exported helpers. `Accepted` reads `Idempotency-Key` into the context for `EnqueueOperation`.
- `API.Validate()` rejects duplicate patterns and operation ids, and operation ids that are missing from or differ in `api/openapi.json`.
- **D3 (settled):** `api/openapi.json` stays the single contract. A new `TestRoutesMatchOpenAPI` requires a one-to-one match between registered routes (core and module) and spec operations.
- The later in-process script caller invokes the same handler with the subject and the transaction-bound service in its context.

## 7. Worker runtime module

### Packages

| Package | Contents |
| --- | --- |
| `internal/worker` | runtime, definitions, validation, activation, capacity, fairness, controls, status, maintenance |
| `internal/worker/admission` | policies (`internal/workflow/admission.go` moves here in Phase 4) |
| `internal/worker/resource` | resource registry and handles |
| `internal/modules/documents` (Phase 3) | document text, optimization, composition |
| `internal/modules/aibatch` (Phase 4) | AI requests and provider coordination |
| `internal/modules/decisions` (Phase 5) | generic and taxonomy decisions |
| `internal/modules/embeddings` (Phase 6) | embedding generation and the sweep |

Each feature module exposes `Register(*worker.Runtime, *dms.Service) error` and an `httpapi.Module`. `cmd/api` composes them in code; there is no plugin framework.

### Configuration

The configuration lives in `config.yaml` (`PAPERGO_CONFIG`, strict fields). Every kind has a built-in default worker:

```yaml
workers:
  maintenance:
    operation_kinds: [field_index, bulk_update, search_reindex, portability.export, portability.import]
    concurrency: 1
  integrations:
    operation_kinds: [integration.refresh]
    concurrency: 2
    retry: {max_attempts: 5, max_elapsed: 6h, backoff: {initial: 30s, max: 30m}}
  documents:
    operation_kinds: [document.text, document.optimize_image, document.compose_pdf]
    concurrency: 1
    execution_timeout: 30m
    resources: [ocr, pdf]
    window: {time_zone: Europe/Madrid, from: "22:00", to: "06:00"}   # optional
  ai-batch:
    operation_kinds: [ai.complete, search.enrich]
    admission:
      policy: threshold_or_max_wait   # immediate | threshold | max_wait | threshold_or_max_wait
      threshold: 100
      max_wait: 2h
      flush: {cron: "0 1 * * *", time_zone: Europe/Madrid, missed: once}
    poll: {every: 5m}
    concurrency: 1
  decisions:
    operation_kinds: [ai.decide, taxonomy.decide]
    admission: {policy: immediate}
    resources: [laya]
    concurrency: 1
resources:
  ocr:   {residency: on_demand, idle_unload: 5m}
  laya:  {residency: on_demand, idle_unload: 5m}
  embeddings:
    residency: warm
    warm: {cron: "0 7 * * 1-5", time_zone: Europe/Madrid, for: 12h}
```

Every worker also accepts these keys, with these defaults:

- `claim.ttl`: 2m;
- `drain_timeout`: 10s;
- `starvation_after`: 10m;
- `priorities: {kind: 0..3}`;
- `pending_policy` (for `disable`): `hold`, or `fail` with `forbidden`.

Startup validation rejects:

- unknown or doubly-owned kinds, and kinds without a worker;
- unknown resources, and resources with conflicting settings;
- an admission policy on a kind without `Admission`;
- a threshold below 1, or a `max_wait` outside 1m–720h;
- a cron without a zone, or one that does not parse with `robfig/cron/v3`;
- a window with `from == to`;
- a `warm` schedule on a resource no worker or synchronous caller registers;
- `concurrency < 1`;
- a priority outside 0–3.

### Activation and admission

- **Wakes:** the enqueue channel per worker; one timer for the earliest of `NextAttempt`, admission deadlines, cron occurrences, window opening, module next actions and deadlines; and the control poll (15 s). A wake only re-evaluates.
- **Immediate:** fair claims up to free capacity.
- **Group policies:** `ReadyGroups` gives `(inputs, oldest)`. A group is eligible when `inputs >= threshold`, `now >= oldest + max_wait`, or a flush occurrence is pending.
  - A deadline or flush writes a drain marker (`worker.drain` / worker / group hash, payload `{cutoff}`) before it claims anything.
  - Passes then claim with `Through = cutoff` until no member is left, and delete the marker. A restart continues an unfinished drain.
  - `Oldest = min(created_at)`, so duplicates, retries and unparked members never move a deadline.
- **Provider limits:** the feature releases claimed members beyond item, byte or token limits. They stay ready, oldest first.
- **Cron:** the occurrence identity is the UTC instant. A `worker.cron` record keeps the last handled occurrence per worker and action. `missed: once | skip`, and at most one catch-up per pass. DST behavior follows `robfig/cron` in the configured zone; tests pin the spring-forward gap and the fall-back repeat, and the user docs state them.
- **Module next actions:** `NextAction`/`Act` handle polling and reconciliation independently of admission and hold no claim or resource.

### Controls

The admin command, `cmd/admin workers {status|pause|resume|drain|unload|disable|enable|flush} <worker>`, writes a `worker.control` record (desired state, plus a flush request id). The runtime polls it every 15 s and on wake.

| Control | Effect |
| --- | --- |
| `pause` | No new claims or admissions. Running executions, renewals, polling, reconciliation and `Expire` continue. |
| `drain` | Same as pause. Executions get `drain_timeout`, then their claims are released (not counted as attempts). |
| `unload` | Unloads the worker's resources that are not held. A held resource unloads on release. |
| `disable` | Same as drain. With `pending_policy: hold`, pending work waits (reason `paused`). With `fail`, it fails with `forbidden`. Nothing is discarded silently. |
| `flush` | One administrative flush occurrence through the normal admission path, with authorization and limits applied. |

The runtime writes a `worker.status` record when the status changes, at most every 15 s:

- queue depth, oldest age, next eligibility and reason;
- active claims, retries and drain progress;
- residency.

`cmd/admin workers status` prints it, and the same fields are logged. There is no new public API.

### Resources

```go
type Resource interface {
    Load(ctx context.Context) error
    Unload() error
}

type Registry struct{ /* ... */ }

func (r *Registry) Register(id string, res Resource, p Policy) error
func (r *Registry) Acquire(ctx context.Context, id string) (release func(), err error)
func (r *Registry) Warm(ctx context.Context, id string) error
func (r *Registry) Unload(ctx context.Context, id string) error
func (r *Registry) Close(ctx context.Context) error
```

- An identity is one engine instance: `ocr`, `pdf`, `laya`, `embeddings`.
- The registry serializes load and unload per identity, and never unloads while a handle is held, even after a lease or timeout expires.
- Inactivity counts from the last release.
- Engines keep their inference gates and lose idle timers and implicit reloads.
- Phase 3 registers all three model engines, deletes `workGate`, and routes every current caller (workflow actions and query embedding) through `Acquire`.

### Shutdown

1. The HTTP server shuts down.
2. `Runtime.Run` stops claiming, waits `drain_timeout`, cancels, releases claims, runs a final status write and closes resources.
3. `cmd/api` waits for it before closing engines and the database.

Maintenance tasks (`RegisterMaintenance(name, every, fn)`) stop the same way.

## 8. Cancellation and supersession

- `POST /v1/operations/{id}/cancel`, for the creator or with `organization.manage`. It returns `409` when the operation is already terminal.
- Cancel, supersede and expire all:
  - clear the claim and `execution_id`;
  - release leases and references;
  - write a live change and call the finished hook.
- **Shared membership:** the provider batch completes for its other members. `FinishMember` skips non-pending members, and valid answers may still fill the reusable cache. Cancellation is terminal at once for the cancelled operation.
- **Running local execution:** its context is cancelled at the next renewal (`ErrClaimLost`). Native inference returns through ONNX Runtime cancellation, and only then is the handle released.

## 9. Failure handling

| Failure | Result |
| --- | --- |
| Invalid payload | Rejected at acceptance (`400`) |
| Quota | Rejected at acceptance (`429`) |
| Idempotency mismatch | Rejected at acceptance (`409`) |
| Authorization revoked | `failed` / `forbidden`, at claim or application |
| `Retry` error (network, provider 5xx/429, busy) | `Defer` with backoff or `Retry-After`; then `retries_exhausted` |
| Execution timeout | `Retry` |
| Unmarked error, panic | `failed`, no code |
| Claim expired | Ready again, counted as an attempt |
| Lost claim | Result discarded |
| Deadline | `failed` / `deadline_exceeded` |
| Ambiguous provider create | Intent stays `uncertain`; reconciled by tag; never re-created from an inconclusive lookup |

## 10. Acceptance tests by phase

| Scenario | Phase | Test home |
| --- | --- | --- |
| Competing workers / expired lease | 1 | `internal/dal/operations` engine tests (SQLite + PostgreSQL) |
| Crash around acceptance or completion | 1 | `internal/dms` rollback of acceptance with leases; terminal release in the same transaction |
| Concurrent API retries / key conflict / key after retention | 1 | DAL unique-index race; `httpapi` replay and `409` |
| Overload at acceptance | 1 | Quota counts per creator, workspace and installation; nothing persisted |
| Source replaced / history collected while queued | 1, 3 | Operation lease blocks reclamation; end-to-end upload during the queue |
| Generated output before crash / retained for review | 1, 3 | Result `object_key` keeps the object while the operation is retained; crash before commit leaves a collectable orphan; run staging rechecks sources; promotion guards unchanged |
| Revoked caller / cancelled / superseded / one shared consumer cancelled | 1, 4 | Fenced finish; member skip with a fake provider |
| Completion deadline (also while parked) | 1, 4 | `Expire` on a fake clock; batch continues |
| Long OCR versus short maintenance | 2 | Blocking fake executor in one worker; `field_index` in another |
| Busy workspace / priority / aging | 2 | Fair claim order on a fake clock |
| Pause / drain / unload / disable / flush | 2 | Control records against a fake module |
| Empty queue / cron / window / retry-not-before | 2 | Policy tests on a fake clock, DST zones included |
| Graceful shutdown and abrupt restart | 2 | Drain timeout; recovery by claim expiry |
| Threshold / maximum wait / restart / overdue backlog | 2, 4 | Policy unit tests; AI module with a fake provider |
| Idle unload / scheduled warm / active inference | 3 | Registry tests; native probe for equivalence after reload |
| Image/PDF processing with OCR capacity one | 3 | No child operation and no second gate |
| Duplicate / incompatible / cached requests | 4 | `ReadyGroups` counts and per-caller results |
| Remote work with an empty queue | 4 | `NextAction` polling with zero claims |
| Provider create with uncertain response | 4 | Moved `submission_test.go` cases |
| HTTP retries inside repeated execution | 4 | Single-attempt client; attempts within budget |
| Decision admission / API and workflow / guards | 5 | Logical-request counts; numeric fixtures; stale taxonomy fingerprint |
| Bulk embedding versus interactive search | 6 | Query latency bound during a sweep |
| API versus current workflow versus later script | 3–6 (script branch later) | Same operation observed by the API and by a workflow wait |

## Phase 1 as implemented

Phase 1 follows the contracts above, with these concrete choices:

- **Configuration.** Quotas come from `OPERATION_QUOTA_PER_CREATOR`, `OPERATION_QUOTA_PER_WORKSPACE`, `OPERATION_QUOTA_INSTALLATION` and `OPERATION_QUOTA_WORKSPACE_PAYLOAD_BYTES` (0 removes a bound), like the other deployment settings. The `workers` section of `config.yaml` arrives with the runtime in Phase 2.
- **Service contract.**
  - `OperationHandler.Execute` replaces `ExternalStep` with the same signature: it receives the claimed operation, whose `Claim` fences its writes.
  - Steps save through `Service.SaveOperation`, which checkpoints a pending operation or finishes a terminal one.
  - `Retry(err, after)` marks transient errors.
  - `Cancelled` is an optional handler hook beside `Failed`.
- **Claims.**
  - `operations.Ready` also takes `IDs`, so a step whose transaction rolled back can claim exactly its operation again to fail or defer it.
  - The loop claims only registered kinds. An operation of a kind no handler serves stays pending with `waiting: queued`; it is not failed.
  - Transactional steps release their claim after each batch without counting an attempt, so other work can run between batches.
  - A transactional step's claim lasts 24 hours, because it never outlives the step's own transaction. A batch that runs longer than the two-minute execution claim therefore still finishes or fails, instead of being claimed again forever.
  - Operations past their deadline are not claimed, and cannot succeed under an existing claim.
- **Routes.** `API.Validate` rejects colliding module routes, reused operation IDs and patterns outside `/v1` at startup. The OpenAPI check is the `TestRoutesMatchOpenAPI` test, because the spec is not compiled into the executable. `httpapi.Accepted` writes the 202, or replayed 200, response for operation-starting routes.
- **Acceptance.** A request whose idempotency key already names the same request is replayed before validation, so a retry still answers with what was accepted after the state it was validated against changed. Index maintenance does not count toward quotas.
- **Search reindex.** The reindex stamp is the operation's acceptance time instead of a payload field, so identical reindex requests have identical payloads and can be replayed by idempotency key.

## Settled decisions

- **D1:** execution columns on `operations`.
- **D2:** `cancelled` status and the cancel route in Phase 1.
- **D3:** single OpenAPI file with a route parity test.
- **D4:** foreign-key owners on `blob_leases`, and `owner_kind` on `activity_references`.
- **D5:** `idempotency_key` and `payload_digest` on `operations`; a binding lives as long as its operation.
- **D6:** `operations.workspace_id`.
- **D7:** `operations.error_code` with `deadline_exceeded`, `retries_exhausted` and `forbidden`.
- **D8:** staging stays workflow-only. Operations return outputs by object reference; runs stage them; optimization and composition have no public routes.
- **D9:** optimization and composition become operations (not workflow actions using only the OCR handle), so their compute and outputs outlive the current workflow engine.

The script-engine branch is not in this checkout. Its document-text code and in-process API were not inspected, and Phase 3 extracts them when the branch is available.
