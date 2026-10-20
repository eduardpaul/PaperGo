# Operation workers and lifecycle policies

Status: **implementation plan**. Implement the foundation on `main`, then integrate it into the script-engine branch. This document proposes the Core sockets for review under the Extension-First Protocol; it does not implement them. The agreed direction is operations independent of workflows, extension-owned workers and feature implementations, and configurable threshold-or-time admission.

The Phase 0 contracts, inventory and Core proposal are in [operation-workers-contracts.md](operation-workers-contracts.md).

## Outcome

An API caller can request OCR or AI processing without creating a workflow. The request produces a durable operation. A configured worker admits and executes it, manages the resources it uses, and records its result. Workflow engines consume the same operation contract and do not own its execution.

The initial deployment stays one executable with embedded workers. Establish execution ownership and recovery on SQLite and PostgreSQL now; add separate worker processes and multi-server deployment later, without changing feature submission contracts.

For example, an AI batch worker can submit when 100 compatible inputs are waiting **or** the oldest waiting input has waited two hours. An OCR worker can run on demand, drain only during a scheduled window, or stay warm during configured hours.

## Baseline and branch order

The baseline inspected for this plan is local `main` at `15733bc`.

Existing pieces on `main`:

- `internal/dms/operationhandlers.go`: registration, transactional validation/enqueue, authorization and external steps.
- `internal/dms/operations.go`: a sequential operation loop with no generic execution claim.
- `internal/dal/activitystore`: namespaced durable records, references, compare-and-swap and leases. Some lease recovery and cleanup currently know about workflow runs.
- `internal/workflow/admission.go`: registered batch admission functions based on ready count and a schedule-due flag.
- `internal/workflowai`: request grouping, durable provider submission intents, reconciliation and polling. Queue consumers are workflow waits and the coordinator is a built-in workflow.
- OCR, local embeddings and Laya: model implementations with their own resource gates and idle eviction behavior.
- `blob_leases`: source-byte retention that requires a workflow run.

The script-engine branch additionally supplies `internal/documenttext`, `POST /v1/items/{id}/text`, an in-process API path, script replay and operation-linked script waits. These are reference implementations to extract or adapt, not dependencies of the work on `main`.

Create implementation branches from the current `main`. Land the foundation and feature cutovers there in dependency order. Then merge the updated `main` into the script-engine branch and reconcile its consumers. Do not start by cherry-picking script-engine commits into `main`: they mix OCR, SDK and interpreter changes.

## Extension-First boundary

| Component | Owns | Does not own |
| --- | --- | --- |
| Core operations | Authorized submission, durable identity/status/result, transactional execution ownership, completion observation and resource retention hooks | Model behavior, provider batching, worker schedule policy or workflow journals |
| Worker runtime module | Worker definitions, activation, admission, execution capacity, resource residency, recovery and shutdown | OCR algorithms, provider protocols or workflow interpretation |
| Feature module | Operation payload/result contract, validation, compatible grouping, compute, checkpoints, model/resource implementation and provider reconciliation | Independent orchestration paths for each caller |
| API / workflow consumer | Submit, observe, consume results and apply caller-specific orchestration | Loading models, running provider coordination or owning the operation queue |

Extensions are compiled modules composed by the executable. Dynamic loading, a plugin marketplace, containers and a separate service are not required.

### Proposed Core sockets

Review concrete signatures, transaction boundaries and schema decisions before implementing Core changes, as required by [AGENTS.md](../../AGENTS.md).

1. **Execution ownership.** Typed service/DAL methods to select supported operation kinds, claim execution, renew/release a claim, defer a next attempt and checkpoint/complete under the current fencing token. Claim selection and ownership must be atomic. Completion, failure and supersession must invalidate stale executors. External computation runs outside the writer; result application verifies ownership inside its transaction.
2. **Completion observation.** Durable terminal-state observation, independent of a particular workflow engine. Reuse existing operation live notifications and transactional events where sufficient. Notifications wake consumers; persisted state and reconciliation establish correctness. Core must not update script journal tables.
3. **Resource ownership.** Durable payload references and source-file leases owned by an operation or module execution, independent of a workflow run. Use a typed owner identity and explicit release/retention rules; do not create fake workflow runs or infer liveness from opaque payloads.
4. **Module routes.** A bounded way for compiled modules to contribute authenticated feature routes and their OpenAPI contracts, using the request's subject and transaction-bound service. Reuse it for external requests and later in-process script requests. Reject duplicate routes/contracts at composition time. Preserve normal authorization, request limits and error handling.

Reuse `operations`, activity records, references and existing notifications wherever they provide the necessary invariants. Do not add a second authoritative job queue or a generic job table by default. Keep policy/provider state in module namespaces. Before adding indexed ownership or due-time fields, demonstrate the query/atomicity requirement that existing records cannot meet and review the smallest generic schema change.

All SQL and Bob construction stay in `internal/dal`. Edit both DAL schema masters directly, regenerate Bob models when shapes change, and use domain types/constants for fixed value sets. Recreate development databases; add no development migrations, backfills or compatibility modes.

## Worker behavior contract

A named worker definition selects its supported operation kinds, queue scope and policies. Configuration belongs to deployment/module composition initially; an administration UI and runtime configuration API are separate work. Validate definitions at startup and reject conflicting ownership or unsupported combinations.

Keep these dimensions independently configurable:

| Dimension | Examples |
| --- | --- |
| Activation | Queue wake, persisted next deadline, cron occurrence, explicit administrative wake |
| Admission | Immediate, threshold, maximum wait, threshold OR maximum wait, scheduled flush |
| Execution window | Always eligible, or admit execution only during a configured window |
| Capacity | Concurrent executions per worker; shared process resource budget |
| Residency | Load on admitted work, stay warm, unload after inactivity, unload after drain, scheduled warming |
| Recovery | Lease lifetime/renewal, retry classification/backoff, deadline, drain timeout |

An activation only reevaluates work. It does not itself require a batch submission or model load. A cron action must explicitly mean enqueue work, flush queued work, or warm resources; these are distinct actions.

Distinguish a shared installation queue from cluster-wide concurrency. Initially enforce configured capacity within the supported single-process deployment. Durable claims prevent concurrent ownership of an operation, but do not by themselves enforce a cluster-wide model/GPU budget. Do not advertise global capacity enforcement until it is implemented and tested.

### Threshold OR maximum wait

Illustrative configuration (final configuration types are a Phase 0 deliverable):

```yaml
workers:
  ai-batch:
    operation_kinds: [ai.complete]
    queue: shared
    admission:
      policy: threshold_or_max_wait
      threshold: 100
      max_wait: 2h
    concurrency: 1
```

Admission operates on compatible, ready, uncached inputs:

- Compatibility is defined by the feature module: queue, provider connection, effective model, request options and any other provider constraints.
- Count distinct provider inputs after authorized deduplication. Retain a separate operation, authorization and completion for each caller. A late duplicate must not reset an existing input's waiting time or inherit another caller's permissions.
- Persist each input's enqueue time. Within a compatible ready group, `deadline = oldest_enqueue_time + max_wait`.
- A nonempty group is eligible when `ready_count >= threshold OR now >= deadline`. Empty groups never submit. A threshold without a timeout and a timeout without a threshold are separately supported policies.
- New arrivals, worker restarts, coordinator polling and transient retries do not reset enqueue times. A provider submission retry keeps its fixed intent and membership.
- Threshold is an admission trigger, not the provider's maximum batch size. Validate feasibility and split eligible work according to provider item/byte/token limits. Oldest eligible inputs go first.
- A deadline flush freezes the membership of the eligible backlog. Drain it in bounded passes even if later passes contain fewer than the threshold; arrivals after that snapshot receive their own deadline. Persist a cutoff/drain marker so a restart does not forget the backlog.
- After claiming/submitting/removing work, recompute the earliest remaining deadline. Changes to worker configuration reevaluate pending work using its original enqueue times. In-flight submission membership remains immutable.
- Persist enough scheduling state to discover overdue groups at startup. In-memory timers and queue notifications are optimizations. Use an injected clock in policy tests; persist absolute UTC instants.
- Maximum wait makes work eligible; it is not a guarantee of provider completion or immediate execution. Capacity, retries and explicitly configured execution windows can delay dispatch. Report these reasons in status/metrics.
- An optional cron flush is another OR condition, not a replacement for maximum wait. Cron requires a time zone, deterministic occurrence identity, explicit missed-occurrence handling and bounded catch-up. Document behavior across daylight-saving transitions.

Provider polling/reconciliation has its own durable next-action time. It continues even when no new batch qualifies for admission. A remote batch waiting for hours must not hold a local execution slot, writer transaction or model resource.

### Resource lifecycle

The runtime owns residency decisions. Feature modules expose safe acquire/warm/release/unload/close behavior and resource identity. One shared resource identity has one lifecycle owner, including when several operation kinds use it.

- Load once when admitted work requires it; reuse across operations and batch members.
- Serialize conflicting load/inference/unload transitions. Never unload during active use.
- Define inactivity from the last resource use, not API status polling or coordinator ticks.
- Remove engine-owned idle timers when the runtime takes ownership. Update every caller and test in that change; retain no alternative lifecycle path.
- Route relevant API and workflow processing through the worker resource owner. Inventory search/query-time embeddings separately: synchronous query inference needs a shared resource handle, not mandatory conversion into a queued operation.
- Graceful shutdown stops new claims, drains within a configured bound, releases resources and leaves unfinished durable work recoverable. Waiting remote work remains durable without an active local execution.

## Implementation phases on main

### Phase 0 — Resolve contracts and approve Core scope

- Inventory operation kinds, producers, existing workflow callers, resource consumers, completion channels, cleanup and retention.
- Specify the four Core sockets, execution state transitions and the proposed storage representation. Demonstrate reuse of activity records first, including renewal/fencing and due-work discovery. Remove workflow-specific assumptions from shared ownership primitives where needed.
- Specify worker configuration validation, queue/group identity, cron/window semantics, policy updates, resource handles and shutdown behavior. Choose module/package boundaries without creating a general plugin framework.
- Specify failure handling: transient execution errors retry; invalid input/authorization failures are terminal; ambiguous remote submissions reconcile before any new create.
- Define cancellation/supersession semantics for claims, shared batch members, result application and resource release. If cancellation requires a public contract/status change, include it in the Core review rather than inventing a module-only terminal status.
- Agree the minimal generic changes before coding them. This phase does not require choosing the future script scheduler or distributed transport.

Exit: reviewed contracts, state-transition diagram, schema rationale and acceptance tests mapped to the phases below.

### Phase 1 — Implement the generic operation sockets

- Implement transactional claims, token-checked renewal/checkpoint/completion, deferral and recovery on both engines.
- Generalize byte/payload retention ownership. Pin the selected source version in the submission transaction and release it according to terminal state and consumer retention rules.
- Expose durable completion observation using existing mechanisms where possible. Cover completion before subscription and recovery after missed notifications.
- Add module route composition and deterministic OpenAPI contribution/verification with authenticated request context. Do not add one feature field to `httpapi.API` per module.
- Keep the operation's public status separate from execution ownership; waiting for a provider or admission does not mean failed.

Exit: two competing executors cannot both commit; an expired executor cannot renew or commit; accepted work and its source bytes survive restart/reclamation without any workflow row.

### Phase 2 — Implement the worker runtime module

- Build named worker registration, queue and timer wakeups, admission policies, bounded execution passes, resource handles, next-action scheduling and graceful shutdown.
- Implement threshold, maximum wait, threshold OR maximum wait and scheduled flush using the semantics above. Keep grouping supplied by the feature module.
- Replace `RunOperations` selection/execution with the runtime. Convert every existing built-in and registered operation handler to the execution contract; retain one execution path. Preserve short transactional batches for database maintenance.
- Give long external work independent capacity so OCR/provider calls do not block field indexing, bulk updates or unrelated operation kinds.
- Move cleanup scheduling out of the removed loop into explicit maintenance registration so retention still runs even when queues are empty.
- Expose queue depth/oldest age, next eligibility, active claims, retries, batch/drain progress and resource residency through existing logs/status conventions.

Exit: fake feature modules demonstrate policy and lifecycle combinations; long blocked external work does not block unrelated operations or hold the SQLite writer.

### Phase 3 — Prove the path with document text/OCR

- Extract the document-text operation behavior from the script-engine branch into a feature module on `main`, including shared extraction settings and result reuse. Do not import the script runtime or generated SDK.
- Register `POST /v1/items/{id}/text` through module routes. Return `202`, the operation and its location; retain the ordinary text-read route for large results.
- Validate/capture source version and OCR settings at acceptance, protect source bytes, compute outside the writer and apply results under a valid claim. Recheck authority/freshness at the boundaries specified in Phase 0.
- Replace OCR engine idle policy with the worker resource lifecycle, including on-demand, maximum idle and scheduled drain/warming cases.
- Make the existing JSON workflow's document-text action a consumer of the operation, using the existing extensible wait/completion facilities. Delete its direct extraction execution path and duplicated result persistence. Any required general completion binding is a consumer of the Phase 1 socket.

Exit: API-only OCR works without workflow records; API and workflow callers use the same execution/lifecycle owner; changing a file during queued work cannot redirect processing to different bytes.

### Phase 4 — Move AI Batch ownership to its module

- Separate provider coordination and durable requests from `internal/workflowai` into an operation module independent of the workflow interpreter. Choose one module namespace and update all callers.
- Replace workflow waits as the authoritative provider input queue with operation-owned requests. Existing workflows submit operations and consume completions; no synthetic runs are created for API callers.
- Provide a bounded, typed feature API for accepted AI requests and results; update OpenAPI and authorization tests together. Credentials and provider selection stay under trusted deployment/module configuration.
- Replace the `ai.batch` built-in coordinator, its workspace startup requirement and workflow-only admission configuration with named worker configuration. Remove the replaced activity, parameter forms, sample usage and documentation in this cutover.
- Apply compatible grouping and threshold OR maximum wait. Keep provider-specific payload limits, immutable submission membership, idempotency/reconciliation, line validation and per-caller authorization in the AI module.
- Preserve the distinction between reusable answer cache and authoritative accepted requests/provider results. Expiring a cache cannot lose accepted work or cause an ambiguous submission to be recreated.
- Update `ai.complete` and `search.enrich` consumers, including fan-out/join and partial success/retry behavior, to use module operations. Delete the direct provider execution paths they replace. Immediate admission uses the same execution machinery.
- Move shared admission policy ownership out of `internal/workflow`; update every caller instead of keeping a forwarding shim.

Exit: both plain API and current workflow callers use the same batch queue; threshold/time admission works across restarts; polling continues with an empty admission queue; ambiguous provider creates are never blindly repeated.

### Phase 5 — Complete lifecycle adoption and main validation

- Apply the established resource lifecycle to local AI/embedding consumers identified in Phase 0. Coordinate shared synchronous inference handles with worker use rather than introducing a second resource policy.
- Remove replaced loops, engine timers, workflow coordinator configuration and provider-specific dependencies in Core. Update startup/shutdown composition, tests, contracts, samples and current-app docs together.
- Verify existing operation kinds, maintenance retention, file reclamation and current workflow behavior after each cutover. Keep the JSON interpreter/DBOS as the current workflow consumer on `main`; their replacement is separate feature work.
- Run repository-required formatting, generation, vet/static analysis and CI checks appropriate to the changed packages, plus the acceptance matrix below. Use existing native-model fixtures/probes for lifecycle and resource checks.

Exit: main provides usable API operations and policy-driven workers independently of the script-engine branch, with one implementation of each replaced behavior.

## Apply to the script-engine branch

After Phases 1–5 land, merge updated `main` into the script-engine branch. Resolve conflicts around the resulting module/operation contracts, not by retaining both implementations.

1. Remove the branch's duplicate document-text registration, `API.DocumentText` dependency and direct model lifecycle wiring. Use the modules composed on `main`.
2. Preserve transactional in-process API execution, but use the same registered routes and request context as external API calls. A script-created operation and its replay step must commit atomically; rollback cannot leave orphan accepted work or source leases.
3. Remove workflow-specific `ReleaseWaits` SQL from the operations repository and both drivers. The script module consumes the generic completion boundary, owns its wait tables, and reconciles recorded waits on resume. Completion before wait registration and missed notifications must both work.
4. Keep the script scheduler responsible for script slices, replay, cancellation and run retention. Worker admission, resource lifetime, provider polling and operation execution remain in the worker/module layer. Suspending a script does not unload another operation's active resources.
5. Regenerate the Kiota client and embedded bundle from the resulting OpenAPI contract. Check exclusion scope, SDK type checking, deterministic generation and bundle tests.
6. Extend script integration tests: submit OCR/AI, suspend, restart, let a worker finish, replay and consume results without repeating submission. Include rejected requests, operation failure and source retention.
7. Update the script-engine plan (on the script-engine branch) to treat this work as its operation foundation. Continue the script lifecycle/scheduler work separately. When the script feature replaces JSON workflows/DBOS, delete those implementations and their consumers/contracts/docs in the same cutover.

## Acceptance matrix

Use deterministic clocks and fake resources/providers for policy and crash tests; real native models are needed only for resource lifecycle/inference validation. Run database ownership/retention tests against SQLite and PostgreSQL using existing DAL test infrastructure.

| Scenario | Required result |
| --- | --- |
| Threshold reached before timeout | Full compatible group is admitted promptly after commit |
| Below threshold until maximum wait | Nonempty partial group is admitted at its original deadline |
| New arrivals or restart before deadline | Original oldest enqueue time remains authoritative |
| Overdue startup / backlog larger than provider maximum | Bounded passes drain the frozen eligible backlog; later arrivals retain their own deadlines |
| Duplicate / incompatible / cached requests | Count eligible distinct inputs per compatible group; preserve caller authorization and results |
| Empty queue / cron / execution window | No empty submissions; deterministic scheduled behavior; deferred work reports why it waits |
| Remote work in flight with no queued inputs | Poll/reconciliation continues without occupying a long-lived execution slot |
| Crash around acceptance or completion | Domain/operation/lease changes commit atomically or roll back together |
| Competing workers / expired lease | At most one valid claim can apply a result; stale attempts cannot commit or renew |
| Provider create with uncertain response | Reconcile the fixed intent; never infer permission to create again from an inconclusive lookup |
| Revoked caller / cancelled or superseded work | Enforce the reviewed authority and terminal-state rules; avoid stale publication and release resources safely |
| Source replaced / history collected while queued | Process the accepted bytes; retained sources survive until the appropriate release boundary |
| Idle unload / scheduled warm / active inference | One resource owner; no unload during use; equivalent inference after reload |
| Long OCR versus short maintenance | Independent capacity; short work and database writes continue |
| Graceful shutdown and abrupt restart | No new claims during drain; unfinished work remains recoverable; resources close safely |
| API versus current workflow versus later script | Same operation implementation, admission policy, resource owner and completion semantics |

## Deferred work

- Separate worker executables, distributed transport, remote blob access and multi-server deployment/capacity enforcement.
- Worker administration UI, public configuration CRUD and arbitrary untrusted worker code.
- Additional operation families such as PDF composition and approvals, except where needed to demonstrate a generic boundary.
- The script engine's own lifecycle, trigger system, scheduler and replacement of JSON workflows/DBOS.

These follow after the single-process operation/worker contract is proven. Do not add feature-specific base tables or speculative distributed infrastructure to anticipate them.
