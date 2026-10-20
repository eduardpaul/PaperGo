# Operation workers: Phase 0 contracts

Status: **proposal for review**. This is the Phase 0 deliverable of [the operation workers plan](operation-workers.md). It inventories `main` at `15733bc`, specifies the four Core sockets, the execution state machine, the storage changes and their rationale, the worker configuration, and maps the acceptance matrix to phases. Nothing here is implemented. The [open decisions](#open-decisions) need an answer before Phase 1 starts.

## 1. Inventory

### Operation kinds

| Kind | Step | Scope | Producers | Terminal side effects |
| --- | --- | --- | --- | --- |
| `field_index` | transactional | none (acts for no one) | `enqueueFieldIndex` (`internal/dms/operations.go:24`) on field indexing changes | `Failed` sets the field `index_failed`; success sets it ready when no other operation is pending |
| `bulk_update` | transactional | `list.write` | `StartBulkUpdate` ← `POST /v1/resources/{id}/bulk-update` | `Failed` clears `operation_items` |
| `search_reindex` | transactional | `organization.manage` | `POST /v1/search/reindex`; `RebuildSearch` at startup as the `papergo` system subject; portability import | Clears the in-memory `searchRebuild` flag |
| `integration.refresh` | **external** | `list.write` | Integration create/update/refresh routes; scheduled `integrations.refresh` built-in workflow | `Failed` writes the integration error. Uses its own integration-row lease (`ErrIntegrationBusy`) |
| `portability.export` / `portability.import` | transactional | `organization.manage` | `RegisterPortability` (the only external `RegisterOperationHandler` caller) ← `POST /v1/portability/{exports,imports}` | `ObjectReferenced` keeps import objects while pending and export objects while their result is unexpired |

All start routes answer `202` with `Location: /v1/operations/{id}`. Reads are `GET /v1/operations/{id}` and `GET /v1/resources/{id}/operations`; the web client polls the former and also receives `operation` frames from `GET /v1/events`.

### Execution today

- `RunOperations` (`internal/dms/operations.go:66`) is one goroutine. It always takes the **oldest** pending operation (`NextOperation`: `status='pending' ORDER BY created_at, id LIMIT 1`). It wakes on a size-1 channel or every minute, and runs operation, delta and live-change retention hourly in the same loop.
- There is no execution claim. A step's exclusivity comes only from the single process and the serial loop.
- **Head-of-line blocking.** When an external step returns `ErrIntegrationBusy`, the loop reports "no work" and sleeps. The next pass selects the same oldest operation again, so every other kind waits until that integration's lease clears. A long external step blocks all kinds the same way. Phase 2 removes both.
- Authorization is rechecked before every step (`operationPermission`), except for `field_index` and the system `search_reindex`.
- Every step error is terminal (`failOperation`); there is no retry classification. A panic is terminal.
- `Supersede` does not write a live notification (`internal/dal/operations/operations.go:176`), unlike `Enqueue` and `Save`.
- `cmd/api/main.go:277-278` starts `RunOperations` and `RunBlobCleanup` without waiting for them on shutdown, so they can still run while deferred closers shut down the engines and the database.

### Workflow callers that become operation consumers

| Workflow action | Engine | Today | Target |
| --- | --- | --- | --- |
| `document.read_text` (`internal/workflow/readtext.go`) | PDFium + OCR | Run/work/commit inside a DBOS step behind the process `workGate`; result in `blob_text` activity records keyed by sha256 + settings | Phase 3: submits a `document.text` operation and waits for it |
| `ai.complete` (batch mode) | Remote batch | Parks an `ai.batch.request` workflow wait; the `ai.batch` built-in coordinator workflow is the queue consumer | Phase 4: submits an `ai.complete` operation |
| `search.enrich` | Remote batch | One non-parking wait per uncached chunk plus an `ai.batch.join` wait | Phase 4: one operation per chunk, joined by the workflow |
| `ai.batch.coordinate` (built-in `ai.batch`, every minute) | — | Leases `ai.batch.state` by run id, admits, submits, polls and reconciles | Phase 4: removed; replaced by the AI worker |
| `file.optimize_image`, `file.compose_pdf` | OCR | Workflow steps behind `workGate` | Phase 5: stay workflow actions and acquire the OCR resource through the runtime |
| `ai.decide`, `taxonomy.decide` | Laya | Workflow steps behind `workGate` | Phase 5: same, through the Laya resource handle |
| `search.embed`, `papergo.search_embed` sweep | Embeddings | Not behind `workGate` | Phase 5: same, through the embedding resource handle |
| Search query (`dms.embedSearch`) | Embeddings | Synchronous on the request path; falls back to keyword on error | Phase 5: synchronous, through the shared embedding handle (not an operation) |

### Resources

| Engine | Concurrency gate | Idle eviction | Close |
| --- | --- | --- | --- |
| PaddleOCR (`internal/ocr/paddle`) | Package-global gate (one inference in the process) | Hard-coded 5 min `time.AfterFunc` | Unloads; reloads on next use |
| Local embeddings (`internal/embedding`) | Per instance | Hard-coded 5 min | Permanent |
| Laya (`internal/decision`) | Per instance | Hard-coded 5 min | Permanent |
| PDFium (`internal/doctext/pdfium`) | Process-wide single-threaded pool | None | None |
| ONNX environment (`internal/onnx`) | — | Never destroyed | — |

`cmd/api` builds one instance of each engine and shares it with every caller. `internal/workflow/tools.go:83` adds a process `workGate` around OCR, optimize, compose and decide, but not around embeddings.

### Completion channels

- `live_changes` rows are written in the writer transaction for `Enqueue` (created) and `Save` (updated). They are delivered by **polling** (`GET /v1/events` reads them every second). There is no LISTEN/NOTIFY.
- Domain events (`internal/dal/events`) are recorded in the same transaction and wake the workflow dispatcher through `Service.OnCommit`. Operations raise none.
- Nothing observes terminal operation states generically; each handler has its own `Failed` callback or follow-up logic.
- Workflow waits are woken by `Runner.SignalWaits` (`ResolveWaits` → `ExpireWaits` → `CompletedParking` → `dbos.Send`) on the dispatcher tick or `Wake`. The only registered resolver is the task resolver.

### Retention and ownership primitives

- `blob_leases(owner_id, run_id → workflow_runs, blob_id)`: every caller is workflow code (an activity job's execution id, or a wait id). `blobProtected` and `ObjectReferenced` honor leases during reclamation.
- `activity_references(owner_id, record_id)`: owners are run ids, wait ids, flight record ids and request record ids. `Purge` deletes references whose owner is a **terminal `workflow_runs` id** (`internal/dal/activitystore/store.go:241`) or an expired activity record.
- `activity_records` leases: `Claim` lets a holder take over when `lease_owner` is a terminal `workflow_runs` id (`store.go:176`). The only lease user is the AI coordinator (holder = run id). Terminal runs already drop their leases explicitly (`CloseRunWaits` → `ReleaseLeases`).
- Operation retention: terminal operations are deleted 30 days after their last update. Pending operations are kept.

### Things the inventory found along the way

- `contentHash` (`internal/workflowai/cache.go:279`) takes a `queue` argument but does not hash it. Phase 4 makes the compatibility key explicit instead.
- The flight status `cleared` is checked (`coordinate.go:889`) but never written.
- No test checks registered HTTP routes against `api/openapi.json`. `TestOpenAPIReferencesResolve` checks only `$ref`s, unique operation ids and GET bodies.

## 2. Execution ownership (socket 1)

### Why the claim lives on `operations`

The plan says to try activity records first. They cannot meet these requirements without becoming a second queue:

1. **Atomic selection and claim.** A worker must pick the oldest *ready* operation of its kinds and own it in one statement. Readiness depends on `operations.status` and `kind`, plus a next-attempt time that does not exist yet. With the claim in `activity_records`, every claim is a two-table read-and-write. On PostgreSQL that needs a lock on rows of both tables, and `FOR UPDATE SKIP LOCKED` cannot skip across a join that writes to the other table.
2. **Due-work discovery.** Startup and timer wakes need "the earliest next attempt among pending operations of kinds K". `activity_records` has no due-time column. Its payload is opaque JSON, and it has no index that could answer that.
3. **One record per operation.** An activity record per operation would duplicate every pending operation in a second table with its own lifecycle and retention: the second authoritative queue the plan forbids.
4. **Fencing on result application.** The terminal write must be conditional on the claim token *in the same row* it updates. Otherwise a stale executor can update the operation in a window where the separate lease row has already moved on.

Activity records stay the right home for **module** state that is not per operation: provider flights, admission drain markers, cron occurrence cursors and worker state. The current lease primitive is enough for that state once its workflow-run takeover rule is removed (see socket 3).

### Schema change (both engines)

Add to `operations`:

| Column | Type | Purpose |
| --- | --- | --- |
| `attempt_at` | `DATETIME NOT NULL` | Earliest instant the operation may be claimed. Set to `created_at` at enqueue; moved by deferral. |
| `attempts` | `INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0)` | Claims that ended without a terminal state. A crash loop is counted. |
| `claim_holder` | `TEXT` | Worker instance that owns execution (`<worker>/<process id>`). |
| `claim_token` | `TEXT` | Fencing token, new on every claim. |
| `claim_until` | `DATETIME` | Expiry. A claim with `claim_until <= now` is void. |
| `execution_id` | `TEXT NOT NULL DEFAULT ''` | Membership in a durable module execution, such as a submitted provider batch. A member is excluded from claims and admission until it is released. |
| `group_key` | `TEXT NOT NULL DEFAULT '' CHECK (length(group_key) <= 128)` | Feature-defined admission compatibility group, computed at acceptance. |
| `input_key` | `TEXT NOT NULL DEFAULT '' CHECK (length(input_key) <= 128)` | Feature-defined deduplication identity of the provider input. Empty means "unique". |

Add these partial indexes, both supported by SQLite and PostgreSQL:

```sql
CREATE INDEX operation_ready ON operations(kind, attempt_at, created_at) WHERE status = 'pending';
CREATE INDEX operation_group ON operations(kind, group_key, created_at) WHERE status = 'pending';
```

`status` gains `cancelled` if [decision D2](#open-decisions) is accepted. The Go `operations.Status` type and both CHECK constraints change together. No other operation column changes: `surface`, `after_item_id` and `prepared` remain handler resume points.

### State machine

Public status stays `pending | succeeded | failed | superseded` (+ `cancelled`). Execution state is a property of a pending operation, derived from the new columns and never a separate status:

```mermaid
stateDiagram-v2
    [*] --> Ready: Enqueue (attempt_at = created_at)
    Ready --> Claimed: Claim (token T, attempts+1)
    Deferred --> Ready: now >= attempt_at
    Claimed --> Claimed: Renew(T) / Checkpoint(T)
    Claimed --> Ready: Release(T) (shutdown, yield; attempts-1) / claim expires
    Claimed --> Deferred: Defer(T, at, cause) (transient error, busy, window)
    Claimed --> Parked: Park(T, E) (joins module execution E)
    Parked --> Ready: Unpark(E) (provider retry)
    Parked --> Deferred: Unpark(E, at)
    Claimed --> Terminal: Finish(T, status)
    Parked --> Terminal: FinishMember(E, status)
    Ready --> Terminal: Supersede / Cancel
    Deferred --> Terminal: Supersede / Cancel
    Claimed --> Terminal: Supersede / Cancel (T invalidated)
    Parked --> Terminal: Supersede / Cancel (member skipped by E)
    Terminal --> [*]: retention (30 days)
```

| Derived state | Condition (status = pending) | Reported reason |
| --- | --- | --- |
| Ready | no live claim, `execution_id = ''`, `attempt_at <= now` | `queued` (the worker status adds `admission`, `capacity` or `window`) |
| Deferred | no live claim, `attempt_at > now` | `retry` when `attempts > 0`, otherwise `scheduled` |
| Claimed | `claim_until > now` | `running` |
| Parked | `execution_id != ''` | `remote` |

`OperationView` gains `waiting` (the reason above), `attempts` and `next_attempt_at`. The reason is computed when the view is read. Writing it on every pass would cost a write per waiting operation per pass.

### Invariants

1. **Only fenced writes change a pending operation.** Every update of a pending operation includes `claim_token = T AND claim_until > now`, or `execution_id = E` for parked members. The exceptions are `Enqueue`, `Supersede`, `Cancel` and `Unpark`. `Save` is replaced by `Checkpoint` and `Finish`; no unfenced save remains.
2. **Terminal transitions clear ownership.** `Finish`, `Supersede` and `Cancel` clear `claim_*` and `execution_id`. A stale executor's next fenced write then affects zero rows and returns `operations.ErrClaimLost`. The executor discards its result and stops.
3. **Terminal transitions release retention in the same transaction.** Operation-owned blob leases and activity references are deleted, and a live change is written. `Supersede` starts writing its live change too.
4. **Claim expiry is recovery.** No startup sweep is needed: an expired claim is simply ready. A claim whose `attempts` already exceeds the worker's `max_attempts` is failed by the next claimer instead of executed.
5. **Clock.** The DAL takes `now` from the caller (the runtime's injected clock), truncated to microseconds and in UTC, like the existing `now()`.

### DAL and service signatures

```go
// internal/dal/operations
var ErrClaimLost = errors.New("operation claim was lost")

type Claim struct {
    Holder, Token string
    Until         time.Time
}

type ClaimRequest struct {
    Kinds    []string
    Holder   string
    TTL      time.Duration
    Now      time.Time
    Limit    int        // operations, or distinct inputs with GroupKey
    GroupKey *string    // nil: any group, ordered by attempt_at, created_at
    Through  *time.Time // admission cutoff: created_at <= Through
}

type Group struct {
    Kind, Key string
    Inputs    int       // distinct non-empty input_key + operations with empty input_key
    Oldest    time.Time // min(created_at) of the group's ready operations
}

// Claim atomically selects and claims ready operations. SQLite: one UPDATE
// ... WHERE id IN (SELECT ...) RETURNING under the writer. PostgreSQL: the
// same with FOR UPDATE SKIP LOCKED in the subquery.
func (r *Repo) Claim(ctx context.Context, in ClaimRequest) ([]Operation, error)
func (r *Repo) Renew(ctx context.Context, id string, c Claim, until time.Time) error
func (r *Repo) Release(ctx context.Context, id string, c Claim) error
func (r *Repo) Defer(ctx context.Context, id string, c Claim, at time.Time, cause string) error
func (r *Repo) Checkpoint(ctx context.Context, op *Operation, c Claim) error
func (r *Repo) Finish(ctx context.Context, op *Operation, c Claim) error
func (r *Repo) Park(ctx context.Context, ids []string, c Claim, executionID string) error
func (r *Repo) Unpark(ctx context.Context, executionID string, ids []string, at time.Time) error
func (r *Repo) FinishMember(ctx context.Context, op *Operation, executionID string) error
func (r *Repo) Cancel(ctx context.Context, id string, at time.Time) error
func (r *Repo) ReadyGroups(ctx context.Context, kinds []string, now time.Time) ([]Group, error)
// NextAttempt is the earliest attempt_at or claim_until among pending,
// unparked operations of the kinds; zero when there are none.
func (r *Repo) NextAttempt(ctx context.Context, kinds []string, now time.Time) (time.Time, error)
```

`Operation` gains `AttemptAt`, `Attempts`, `Claim`, `ExecutionID`, `GroupKey` and `InputKey`. `Next` and `Save` are deleted.

In `internal/dms`, `OperationHandler` keeps `Scope`, `Validate` and `Failed`. It changes as follows:

```go
type OperationHandler struct {
    Scope    string
    Validate func(context.Context, *Service, string, string, json.RawMessage) error
    // Admission returns the compatibility group and input identity of an
    // accepted payload. Nil means no grouping.
    Admission func(json.RawMessage) (group, input string, err error)
    // Step runs one bounded batch inside the claim's write transaction.
    Step func(context.Context, *Service, *operations.Operation) error
    // Execute runs outside the writer. It applies results with
    // Service.FinishOperation/CheckpointOperation under its claim.
    Execute func(context.Context, *Service, *Execution) error
    Failed  func(context.Context, *Service, *operations.Operation, error) error
}

// Retry marks err as transient; the worker defers the operation by its
// backoff, or by after when it is positive.
func Retry(err error, after time.Duration) error
```

`ExternalStep` is replaced by `Execute`. `integration.refresh` returns `Retry(ErrIntegrationBusy, leaseRemaining)` instead of blocking the queue. Errors that are not marked with `Retry` stay terminal, as today, and so do authorization failures and invalid payloads. Context cancellation during shutdown is neither: the runtime calls `Release`.

**Transactional steps** (`field_index`, `bulk_update`, `search_reindex`, `portability.*`) claim, step and either finish or release in **one** write transaction. On SQLite the writer slot makes this exclusive. On PostgreSQL the claim's `SKIP LOCKED` row lock does. Their batches stay as short as they are today.

**External executions** claim in one short transaction. They compute outside the writer while a heartbeat renews the claim at a third of its TTL, then apply the result in a transaction that starts with the fenced `Finish` or `Checkpoint`. A heartbeat failure cancels the execution's context.

### Authority and freshness boundaries

| Boundary | Check |
| --- | --- |
| Acceptance | `operationPermission` + `Validate` in the enqueue transaction, as today. Feature modules capture the source version (blob id) and lease it there. |
| Claim / step | `operationContext` + `operationPermission` before each step or execution, as today, with the same two system exceptions. |
| Result application | The fenced `Finish` transaction rechecks `operationPermission`. A revoked caller → `failed` (forbidden). This affects only that operation, never other batch members. |
| Batch admission | Each member is rechecked when admitted. Revoked members fail individually before submission. |

## 3. Resource ownership (socket 3)

### Blob leases

`blob_leases.run_id` becomes nullable and gains a sibling owner column. Exactly one of the two is set:

```sql
run_id TEXT REFERENCES workflow_runs(id) ON DELETE CASCADE,
operation_id TEXT REFERENCES operations(id) ON DELETE CASCADE,
CHECK ((run_id IS NULL) <> (operation_id IS NULL))
```

plus `CREATE INDEX bloblease_operation_id ON blob_leases(operation_id)`. Foreign keys give both owners typed identity and deletion safety without inferring liveness from strings. `blobs.Repo` gains `LeaseOperation(ctx, operationID, blobIDs)`, and `operations.Repo` releases operation leases inside terminal transitions. Reclamation (`blobProtected`, `ObjectReferenced`) is unchanged because it only asks whether a lease exists.

Release rule: a source lease lives from acceptance to the operation's terminal transition. Consumers that need the bytes afterwards read the stored result (for document text, `blob_text` is keyed by content hash and does not depend on the source blob), so no consumer retention is needed beyond terminal state. If a later consumer needs one, it leases under its own owner before the operation finishes.

### Activity references and leases

`activity_references` gains `owner_kind TEXT NOT NULL CHECK (owner_kind IN ('run', 'wait', 'record', 'operation'))` with a Go `activitystore.OwnerKind` type. The unique key becomes `(owner_kind, owner_id, record_id)`. `Reference`, `Unreference` and `Release` take the kind. `Purge` replaces its hidden assumption with one explicit rule per kind:

| Owner kind | Reference is dropped when |
| --- | --- |
| `run` | the run is terminal (current rule) or deleted |
| `wait` | the wait is closed or deleted (`releaseWait` releases explicitly; Purge is the backstop) |
| `record` | the owning record expired (current rule) |
| `operation` | the operation is terminal (released in its terminal transaction; Purge is the backstop) |

`Claim` drops the `EXISTS(SELECT 1 FROM workflow_runs ...)` takeover clause, so expiry is the only takeover rule. Terminal runs already release their leases explicitly in `CloseRunWaits`, so no behavior is lost.

## 4. Completion observation (socket 2)

- **Durable state is the truth.** A consumer records the operation id in its own durable state (a workflow wait, and later a script journal), then reads `operations` to learn the outcome. "Completed before subscription" therefore needs no special case: the consumer checks the operation after recording its interest.
- **Wake-up.** `dms.Service` gains `OnOperationsFinished(func(ids []string))`. It is called after commit for transactions that moved operations to a terminal status, the same way `OnCommit` works today. `cmd/api` wires it to `Runner.Wake`. Live changes (now also written by `Supersede` and `Cancel`) keep serving SSE clients.
- **Missed wake-ups.** Consumers reconcile on their own tick. `operations.Repo.Finished(ctx, ids []string) ([]Operation, error)` returns those of the given ids that are terminal, in one query.
- **Workflow consumer (Phase 3/4).** `internal/workflow` registers a wait resolver for kind `operation` (key = operation id). It uses `Finished` and completes the wait with `{status, result, error}`. Core never touches `workflow_waits`, and later the script module adds its own resolver for its own tables.
- Operations do **not** raise domain events. Field index and reindex operations are frequent, and the event log is for item-level triggers. If a workflow trigger on operation completion is wanted later, it is a separate decision.

## 5. Module routes (socket 4)

```go
// internal/httpapi
type Module interface {
    Name() string
    Routes() []Route
}

type Route struct {
    Method, Pattern string // "POST", "/v1/items/{id}/text"
    OperationID     string // must exist in api/openapi.json under Method+Pattern
    Write           bool   // reads get RequestTimeout like core reads
    Handle          func(http.ResponseWriter, *http.Request, Request) error
}

type Request struct {
    Subject string
    DMS     *dms.Service
}

// Decode, Accepted (202 + Location: /v1/operations/{id} + OperationView)
// and JSON are exported helpers over the existing decode/problem code.
```

- `API` gains one field, `Modules []Module`. `Handler()` registers module routes on the same `/v1` mux, behind `authenticate` and `limiter`, after the core routes. A returned error goes through `failure()`, so error mapping stays central.
- `API.Validate()` runs at startup (and in tests). It rejects a duplicate `Method+Pattern` against core or another module (by catching `ServeMux` pattern conflicts), a duplicate `OperationID`, and an `OperationID` that is missing from `api/openapi.json` or differs from its method and path.
- **Contract**: `api/openapi.json` remains the single published contract (Kiota and the web client read it). Modules add their paths there in the same change, as core features do today. A new `TestRoutesMatchOpenAPI` enumerates every registered route (core and module) and every spec operation and requires a one-to-one match. That is the deterministic verification; see [decision D3](#open-decisions) for the generated-fragment alternative.
- An in-process caller (the later script engine) invokes the same `http.Handler` with a context that carries the authenticated subject and the caller's transaction-bound service. The route code is identical. This is specified now and implemented on the script branch.

## 6. Worker runtime module

Packages:

- `internal/worker`: runtime, definitions, configuration validation, activation, capacity, recovery, status.
- `internal/worker/admission`: policies. `internal/workflow/admission.go` moves here in Phase 4, and its callers are updated.
- `internal/worker/resource`: resource registry and handles.
- Feature modules: `internal/modules/documenttext` (Phase 3) and `internal/modules/aibatch` (Phase 4). Each exposes `Register(*worker.Runtime, *dms.Service)` plus an `httpapi.Module`.

No plugin framework: `cmd/api` composes the modules in code.

### Configuration

Workers live in the existing `config.yaml` (`PAPERGO_CONFIG`; parsed with `KnownFields(true)`). Built-in defaults exist for every kind, so a deployment without a `workers` section behaves well:

```yaml
workers:
  maintenance:                      # default
    operation_kinds: [field_index, bulk_update, search_reindex, portability.export, portability.import]
    concurrency: 1
  integrations:                     # default
    operation_kinds: [integration.refresh]
    concurrency: 2
    claim: {ttl: 5m}
    retry: {max_attempts: 5, backoff: {initial: 30s, max: 30m}}
  document-text:                    # Phase 3 default
    operation_kinds: [document.text]
    concurrency: 1
    resources: [ocr.paddle, pdf.pdfium]
    window: {time_zone: Europe/Madrid, from: "22:00", to: "06:00"}   # optional
  ai-batch:                         # Phase 4
    operation_kinds: [ai.complete, search.enrich]
    admission:
      policy: threshold_or_max_wait # immediate | threshold | max_wait | threshold_or_max_wait
      threshold: 100
      max_wait: 2h
      flush: {cron: "0 1 * * *", time_zone: Europe/Madrid, missed: once}  # optional extra OR
    poll: {every: 5m}
    concurrency: 1
resources:
  ocr.paddle:
    residency: on_demand            # on_demand | warm
    idle_unload: 5m
    unload_after_drain: false
    warm: {cron: "45 21 * * *", time_zone: Europe/Madrid, for: 9h}    # optional
```

Startup validation rejects the following:

- an unknown kind, or a kind owned by two workers;
- a kind no worker owns;
- an unknown resource;
- an admission policy on a kind whose handler has no `Admission` function;
- a threshold below 1, or a `max_wait` outside 1m–720h;
- a cron without a time zone, or one that does not parse with `robfig/cron/v3` (already a dependency);
- a window whose `from` equals `to`;
- a `warm` schedule on a resource no worker uses;
- capacity below 1.

### Activation and admission

- **Wake sources.** The enqueue channel (one per worker), the earliest of `NextAttempt`, admission deadlines, cron occurrences and module next-action times (one timer per worker), and administrative wake. A wake only re-evaluates.
- **Immediate.** Claim ready operations up to free capacity.
- **Group policies.** `ReadyGroups` reports `(inputs, oldest)` per group. A group is eligible when `inputs >= threshold` (threshold policies), when `now >= oldest + max_wait` (max-wait policies), or when a flush occurrence is pending.
  - A deadline or flush records a drain marker (`activity_records` namespace `worker.drain`, scope = worker, key = group hash; payload `{cutoff}`) before it claims anything.
  - Passes then claim with `Through = cutoff` until no ready member remains, and delete the marker. Arrivals after the cutoff keep their own `created_at` and so their own deadline.
  - A restart finds the marker and continues.
  - Because `Oldest` is `min(created_at)` over the group, a late duplicate (same `input_key`) never moves a deadline. A retried or unparked member keeps its `created_at`.
- **Provider limits.** The claim takes up to the feature's maximum number of distinct inputs. The feature may `Release` claimed members that exceed byte or token limits. They stay ready, oldest first, and keep their deadlines.
- **Cron.** The occurrence identity is the scheduled UTC instant. The last handled occurrence per worker and action is stored in `worker.cron` activity records. `missed: once` coalesces any number of missed occurrences into one; `missed: skip` drops them. Catch-up never runs more than one occurrence per pass. Across DST, occurrences are computed by `robfig/cron` in the configured zone; tests pin the exact behavior for the spring-forward gap and the fall-back repeat, and the user docs state it.
- **Execution window.** Outside the window, ready work is not claimed. Worker status reports `window` with the next opening, and the timer is set to it.
- **Configuration change.** Nothing is stored per policy. A new configuration re-evaluates the same `created_at` values, and parked members are unaffected.
- **Module next actions.** A feature can register `NextAction(ctx) (time.Time, error)` and `Act(ctx)` per worker for work that is not a claim, such as provider polling and reconciliation. These run on the worker's timer whether or not anything is admissible, and they hold no claim or resource while waiting.

### Capacity

Each worker has its own execution slots, so OCR cannot occupy the maintenance worker. Transactional steps still share the single writer slot. They take it with `BackgroundWrite` priority, like workflow steps, so people's edits go first; today's operation loop takes it as an ordinary writer. The capacity is per process; the docs say so and do not claim cluster-wide limits.

### Resources

```go
// internal/worker/resource
type Resource interface {
    Load(ctx context.Context) error
    Unload() error // never called while a handle is held
}

type Registry struct{ /* ... */ }

func (r *Registry) Register(id string, res Resource, policy Policy) error
// Acquire loads if needed and blocks unload until release. Synchronous
// callers (search queries, workflow actions) use it too.
func (r *Registry) Acquire(ctx context.Context, id string) (release func(), err error)
func (r *Registry) Warm(ctx context.Context, id string) error
func (r *Registry) Close(ctx context.Context) error
```

- The registry serializes load and unload per id. Inactivity counts from the last `release`, not from status reads or ticks.
- Engines keep their inference gates, which are needed for thread safety. They lose `idleTimeout`, `armIdle`/`release` timers and implicit reloading. `Unload` releases sessions and calls `nativeheap.Release`; `Load` opens them. The engines' existing tests move to the registry tests.
- Phase 3 does this for OCR and Phase 5 for embeddings and Laya. Each cutover updates every caller (`workGate` users included) in the same change.

### Shutdown

`Runtime.Run(ctx)` returns only after the following steps, and `cmd/api` waits for it before closing engines and the database:

1. Stop claiming.
2. Wait up to `drain_timeout` (default 10s) for executions.
3. Cancel the rest and `Release` their claims (not counted as attempts).
4. Close resources.

The HTTP server shuts down first, as today. `RunBlobCleanup` and the retention passes become maintenance tasks registered with the runtime (`RegisterMaintenance(name, every, fn)`), so they run with empty queues and stop the same way.

### Status

`GET /v1/organization/workers` (requires `organization.manage`; added to OpenAPI in Phase 2) reports per worker:

- queue depth, oldest age, next eligibility and reason;
- active claims and retries;
- drain progress;
- resource residency.

Logs use the existing `slog` keys.

## 7. Cancellation and supersession

- `POST /v1/operations/{id}/cancel` (proposed; [D2](#open-decisions)) is allowed for the creator, or with `organization.manage`. It sets `cancelled` on a pending operation and returns `409` on a terminal one.
- Supersede (system replacement) and cancel (caller withdrawal) behave the same way:
  - they clear the claim, which fences out the executor;
  - they release operation-owned leases and references;
  - they write a live change and call the finished hook.
- **Shared batch member.** The provider batch keeps its immutable membership. When it completes, `FinishMember` finds the member no longer pending and skips it. Valid answers still fill the reusable answer cache, which is not caller-specific. Other members are unaffected.
- **Resource release.** A cancelled executor's context is cancelled at its next renewal (`ErrClaimLost`), and the deferred handle release runs. Inference that is in progress is not interrupted beyond what the engine's own context support allows; ONNX Runtime `Run` honors cancellation.

## 8. Failure handling

| Failure | Classification |
| --- | --- |
| Invalid payload, failed authorization, revoked credential | Terminal at acceptance or claim |
| Error marked `Retry` (network, provider 5xx/429, `ErrIntegrationBusy`) | `Defer` with backoff until `max_attempts`, then `failed` with the last cause |
| Unmarked error, panic | Terminal (current behavior) |
| Claim expired (crash, stall) | Ready again, counted as an attempt |
| Lost claim during execution | Result discarded; nothing written |
| Ambiguous provider create | The AI module's fixed intent stays `uncertain` and is reconciled by tag. A create is only ever made from `prepared`, and an inconclusive lookup leaves it `uncertain` (current rule, kept) |

## 9. Acceptance tests by phase

| Scenario | Phase | Test home |
| --- | --- | --- |
| Competing workers / expired lease | 1 | `internal/dal/operations` engine tests (SQLite + PostgreSQL): two claimers, one wins; expired token cannot `Renew`/`Finish` |
| Crash around acceptance or completion | 1 | `internal/dms`: an enqueue with a lease rolls back as a unit; a terminal transition releases leases and references in the same transaction |
| Source replaced / history collected while queued | 1, 3 | DAL lease test (an operation-owned lease blocks reclamation); Phase 3 end-to-end with a new upload during the queue |
| Revoked caller / cancelled or superseded work | 1, 4 | Fenced-finish tests; batch member skip test with a fake provider |
| Long OCR versus short maintenance | 2 | Fake blocking executor in one worker; `field_index` completes in another |
| Graceful shutdown and abrupt restart | 2 | Runtime test with drain timeout; kill-and-restart recovers by claim expiry |
| Empty queue / cron / execution window | 2 | Policy tests on a fake clock, including DST zones |
| Threshold reached before timeout | 2, 4 | Policy unit test; AI module with a fake provider |
| Below threshold until maximum wait | 2, 4 | Same, on a fake clock |
| New arrivals or restart before deadline | 2, 4 | Restart from a persisted state; deadline unchanged |
| Overdue startup / backlog larger than provider maximum | 2, 4 | Drain marker survives restart; bounded passes |
| Duplicate / incompatible / cached requests | 4 | `ReadyGroups` counts and per-caller results |
| Remote work in flight with no queued inputs | 4 | Poll runs from `NextAction` with zero claims |
| Provider create with uncertain response | 4 | Existing `submission_test.go` cases, moved |
| Idle unload / scheduled warm / active inference | 3, 5 | Registry tests with fake resources; native probe for equivalence after reload |
| API versus current workflow versus later script | 3, 4 (script branch later) | Same operation id observed by the API and by a workflow wait |

## Open decisions

- **D1 — Execution columns on `operations`.** Add the eight columns and two partial indexes in §2 instead of keeping claims in `activity_records`. *Recommended*, for the four reasons listed there.
- **D2 — Public cancellation.** Add the `cancelled` status and `POST /v1/operations/{id}/cancel` in Phase 1. *Recommended*: the acceptance matrix requires cancelled work, and `superseded` means system replacement, not caller withdrawal. The alternative is to defer cancellation and ship only supersession.
- **D3 — OpenAPI ownership.** Keep the single hand-maintained `api/openapi.json` with a route-to-spec parity test (*recommended*: it adds no generator and covers core routes too). The alternative is per-module fragment files merged by a generator, with a test that the merge is up to date.
- **D4 — Typed owners.** Use nullable `run_id`/`operation_id` foreign keys on `blob_leases`, and `owner_kind` on `activity_references` with per-kind purge rules. *Recommended*. The alternative, `owner_kind` on both, loses foreign-key cascades on `blob_leases`.

Once these are answered, Phase 1 can start on a branch from `main`.

The script-engine branch is not in this checkout, so its document-text code and in-process API were not inspected. Phase 3 extracts them when that branch is available.
