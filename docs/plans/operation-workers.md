# Operation workers and lifecycle policies

Status: **implementation plan**. Implement the foundation on `main`, then integrate it into the script-engine branch. This document proposes the Core sockets for review under the Extension-First Protocol; it does not implement them. The agreed direction is operations independent of workflows, extension-owned workers and feature implementations, and configurable threshold-or-time admission.

The Phase 0 contracts, inventory and Core proposal are in [operation-workers-contracts.md](operation-workers-contracts.md).

## Outcome

An API caller can request OCR, LLM processing or local decisions without creating a workflow. The request produces a durable operation. A configured worker admits and executes it, manages the resources it uses, and records its result. Workflow engines consume the same operation contract and do not own its execution. Local Laya decisions (`ai.decide` and `taxonomy.decide`) are first-class consumers of this foundation, including configurable admission and model residency.

The initial deployment stays one executable with embedded workers. Establish execution ownership and recovery on SQLite and PostgreSQL now; add separate worker processes and multi-server deployment later, without changing feature submission contracts.

For example, an AI batch worker can submit when 100 compatible inputs are waiting **or** the oldest waiting input has waited two hours. OCR and decision workers can run on demand, drain only during a scheduled window, stay warm during configured hours, or use the same threshold-or-maximum-wait policy.

## Baseline and branch order

The baseline inspected for this plan is local `main` at `15733bc`.

Existing pieces on `main`:

- `internal/dms/operationhandlers.go`: registration, transactional validation/enqueue, authorization and external steps.
- `internal/dms/operations.go`: a sequential operation loop with no generic execution claim. It always selects the oldest pending operation, so one busy or long external step blocks every other kind.
- `internal/dal/activitystore`: namespaced durable records, references, compare-and-swap and leases. Some lease recovery and cleanup currently know about workflow runs.
- `internal/workflow/admission.go`: registered batch admission functions based on ready count and a schedule-due flag.
- `internal/workflowai`: request grouping, durable provider submission intents, reconciliation and polling. Queue consumers are workflow waits and the coordinator is a built-in workflow.
- `internal/workflow/decisions.go`: preparation, model execution, caching and guarded completion for `ai.decide` and `taxonomy.decide`; `internal/decision` supplies Laya inference and deterministic native batching.
- `internal/workflow/optimize.go` and `internal/workflow/compose.go`: existing image optimization and searchable PDF composition, both using OCR and producing staged files. Their shared-resource adoption is part of this plan.
- `internal/workflow/embed.go`: immediate generation embeddings and a recovery sweep scheduled by DBOS; `internal/dms/searchquery.go` also uses embeddings for interactive search.
- `internal/ai/http.go`: provider requests retried in-process (four attempts, sleeping up to 30 seconds per `Retry-After`); this loop must give retry ownership to the worker.
- OCR, local embeddings and Laya: model implementations with their own resource gates and hard-coded five-minute idle eviction. `internal/workflow/tools.go` adds a second process-wide `workGate` around OCR, optimization, composition and decisions.
- `blob_leases` and `staged_files`: source-byte retention and staged outputs whose schema requires a workflow run (`run_id NOT NULL`); staged promotion and discard are authorized against that run.
- `Runner.Purge`: activity record purge and result cache expiry run only inside a DBOS-scheduled workflow-runner job.

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

1. **Submission identity and execution ownership.** Typed service/DAL methods to atomically bind an optional caller-scoped idempotency key to an accepted operation, select supported operation kinds, claim execution, renew/release a claim, defer a next attempt and checkpoint/complete under the current fencing token. Key binding and acceptance must share a transaction, as must claim selection and ownership. Completion, failure and supersession must invalidate stale executors. External computation runs outside the writer; result application verifies ownership inside its transaction.
2. **Completion observation.** Durable terminal-state observation, independent of a particular workflow engine. Reuse existing operation live notifications and transactional events where sufficient. Notifications wake consumers; persisted state and reconciliation establish correctness. Core must not update script journal tables.
3. **Resource ownership.** Durable payload references and source-file, temporary-output and staged-artifact ownership by an operation or module execution, independent of a workflow run. This includes `blob_leases`, `staged_files` and `activity_references`. Use a typed owner identity and explicit release/retention rules, including transfer to a result consumer or review; do not create fake workflow runs or infer liveness from opaque payloads.
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
| Capacity | Concurrent executions per worker |
| Fairness and overload | Per-workspace/caller queue limits, bounded priority, shared-provider rate limits |
| Residency | Load on admitted work, stay warm, unload after inactivity, unload after drain, scheduled warming |
| Recovery | Lease lifetime/renewal, retry classification/backoff, deadline, drain timeout |

An activation only reevaluates work. It does not itself require a batch submission or model load. A cron action must explicitly mean enqueue work, flush queued work, or warm resources; these are distinct actions.

Distinguish a shared installation queue from cluster-wide concurrency. Initially enforce configured capacity within the supported single-process deployment. Durable claims prevent concurrent ownership of an operation, but do not by themselves enforce a cluster-wide model/GPU budget. Do not advertise global capacity enforcement until it is implemented and tested.

### Threshold OR maximum wait

Illustrative configuration (the configuration types are specified in the Phase 0 contracts):

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

- Compatibility is defined by the feature module: queue, provider connection or local resource identity, effective model/artifact, request options and any other execution constraints.
- Count distinct execution inputs after authorized deduplication. Retain a separate operation, authorization and completion for each caller. A late duplicate must not reset an existing input's waiting time or inherit another caller's permissions. Feature modules specify the counted unit; decision admission counts logical decision requests, not their expanded questions or taxonomy candidates.
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

### Submission identity, fairness and overload

- Accept `Idempotency-Key` on every operation-producing API, Core and module alike, through the shared enqueue path. Scope it to the authenticated principal, operation kind and authorized container (or the organization). Persist the normalized payload digest and key binding atomically with acceptance. Concurrent matching retries return the same operation; changed payloads conflict. Apply current authorization when resolving a key, and expose no other caller's operation or provider state.
- A key binding lives exactly as long as its operation: while pending and through operation retention after it finishes. After retention the key can represent a new submission. Document key size, this window and the conflict response in the API contract.
- Idempotent submission preserves one caller's intent. Authorized input deduplication shares computation across distinct intents; each operation keeps independent authorization, cancellation and completion.
- Attribute every operation to its workspace (or to the organization) at acceptance. Configure bounded pending operation counts and payload bytes per caller/workspace and installation. Reject overload with `429` and `Retry-After` before accepting work; accepted operations must remain durable under later overload.
- Dispatch eligible work fairly across workspaces and compatible groups, using bounded priorities/aging so one busy tenant or interactive stream cannot starve others. Fairness chooses between groups and claims; it does not reorder the membership of a group or of a frozen drain, which stays oldest first, and it does not move oldest-wait deadlines.
- Coordinate rate limits and `Retry-After` across workers sharing a provider connection/credential. Persist long deferrals and release execution slots while waiting. Cluster-wide rate enforcement belongs to the later distributed phase.

### Time budgets and retry ownership

Treat these clocks separately:

| Clock | Meaning |
| --- | --- |
| Maximum admission wait | Time from original enqueue until a group becomes eligible |
| Completion deadline | Latest acceptable successful completion of an operation |
| Execution timeout | Allowed duration of one active execution attempt |
| Retry budget | Total attempts and elapsed retry allowance, including provider-client retries |
| Execution lease | Current executor's authority, renewed independently of admission timing |
| Inactivity timeout | Time since last actual resource use before idle unloading |

An execution window gates new execution even after maximum wait is reached; report the next window and overdue eligibility. An exhausted completion deadline ends successful-publication eligibility regardless of window or retry state: the operation fails with a distinct deadline error code, without a new public status. Persist retry-not-before times to avoid a past admission deadline causing a hot retry loop.

The worker owns retries. Under a worker, provider clients make a single attempt (with at most short jitter) and report `Retry-After` and retryability as typed errors; the worker persists the deferral and releases the slot. Classify invalid requests and revoked authorization as terminal. Reconcile uncertain provider writes before another create. A repeatedly failing operation must exhaust its bounded budget without blocking the queue.

Native execution timeouts use supported cooperative cancellation. Keep resources held until native work actually returns; an expired lease or timeout does not make unloading active sessions safe. Process isolation and hard memory/termination guarantees remain outside the initial embedded runtime.

### Resource lifecycle

The runtime owns residency decisions. Feature modules expose safe load/unload behavior and resource identity. One shared resource identity — one engine instance — has one lifecycle owner, including when several operation kinds and synchronous callers use it.

- Load once when admitted work requires it; reuse across operations and batch members.
- Serialize conflicting load/inference/unload transitions. Never unload during active use.
- Define inactivity from the last resource use, not API status polling or coordinator ticks.
- Remove engine-owned idle timers and the workflow `workGate` when the runtime takes ownership. Update every caller and test in that change; retain no alternative lifecycle path.
- Route relevant API and workflow processing through the worker resource owner. Synchronous query-time embedding uses a shared resource handle, not a queued operation.
- Graceful shutdown stops new claims, drains within a configured bound, releases resources and leaves unfinished durable work recoverable. Waiting remote work remains durable without an active local execution.

Process-wide CPU/memory budgets and reservations across resources are deferred; capacity is bounded per worker.

For local decisions, worker admission and native inference batching are separate. The worker decides when queued requests run; the decision engine still enforces its row/token limits and deterministic question/candidate order. The admission threshold is unrelated to the probability threshold used to select taxonomy terms. Both `ai.decide` and `taxonomy.decide` share one Laya resource owner.

### Nested work, cancellation and controls

- Image/PDF processing uses the shared OCR resource handle in-process; it does not submit child operations. Document-backed decisions require already extracted text; a caller that needs text first submits document text and then the decision. No operation in scope depends on a durable child operation, so the runtime gets no dependency or DAG machinery. If a later module needs one, review it then.
- Cancel or supersede one consumer independently of shared inference/provider membership. Suppress its result publication and release its references. Continue work required by other authorized consumers. Shared provider work is completed for other members and permitted cache reuse; cancellation is terminal immediately for the cancelled operation.
- Give worker controls distinct meanings: pause stops new admission, drain completes/relinquishes active execution, unload releases idle resources, and disable stops feature execution according to an explicit pending-work policy. None silently discards accepted operations.
- Continue required lease renewal, remote reconciliation and result collection while admissions are paused or draining. Honor terminal deadlines. Administrative flush reevaluates compatible queued work through the same admission/execution path; it cannot bypass authorization or limits. Expose controls through initial deployment/process tooling; a new public administration API/UI remains deferred.

### Generated artifacts and retention

Track source bytes, temporary outputs and published staged results as separate ownership boundaries. Protect output production and the interval between object storage and fenced result commit; define provisional staging ownership and recovery of writes left by a crash. Do not rely solely on the one-hour orphan-object grace period for long work.

Transfer or add a durable reference when an output becomes an operation result or awaits review. A workflow ending, operation retention expiring or one consumer cancelling must not delete an artifact still retained by another consumer. Promotion to a current file must use the ordinary guarded domain write and atomically update its ownership. Failed/stale attempts may not publish artifacts; cleanup eventually collects their unreferenced objects. Apply this contract to staged PDFs, optimized images and durable provider input/output files as appropriate.

## Implementation phases on main

### Phase 0 — Resolve contracts and approve Core scope

- Inventory operation kinds, producers, existing workflow callers, resource consumers, completion channels, cleanup and retention.
- Specify the four Core sockets, execution state transitions and the proposed storage representation. Demonstrate reuse of activity records first, including renewal/fencing and due-work discovery. Remove workflow-specific assumptions from shared ownership primitives where needed.
- Specify worker configuration validation, queue/group identity, cron/window semantics, policy updates, resource handles and shutdown behavior. Choose module/package boundaries without creating a general plugin framework.
- Specify API idempotency scope/retention, queue quotas, workspace attribution, fair dispatch, worker control states and all six clocks above. Enumerate existing OCR/image/PDF and interactive/background embedding callers, plus DBOS and standalone maintenance schedules, so every cutover has an owner.
- Specify failure handling: transient execution errors retry; invalid input/authorization failures are terminal; ambiguous remote submissions reconcile before any new create.
- Define cancellation/supersession semantics for claims, shared batch members, result application and resource release. If cancellation requires a public contract/status change, include it in the Core review rather than inventing a module-only terminal status.
- Agree the minimal generic changes before coding them. This phase does not require choosing the future script scheduler or distributed transport.

Exit: reviewed contracts, state-transition diagram, schema rationale and acceptance tests mapped to the phases below.

### Phase 1 — Implement the generic operation sockets

- Implement transactional claims, token-checked renewal/checkpoint/completion, deferral, cancellation and recovery on both engines.
- Generalize byte/payload/staged-output ownership to typed owners. Pin the selected source version in the submission transaction and release it according to terminal state and consumer retention rules.
- Implement atomic idempotent acceptance, workspace attribution, completion deadlines and fenced output publication, including provisional artifact ownership and retained result references. Verify payload conflicts, concurrent retries and retention boundaries through the typed DAL methods.
- Expose durable completion observation using existing mechanisms where possible. Cover completion before subscription and recovery after missed notifications.
- Add module route composition and deterministic OpenAPI verification with authenticated request context. Do not add one feature field to `httpapi.API` per module.
- Keep the operation's public status separate from execution ownership; waiting for a provider or admission does not mean failed.

Exit: two competing executors cannot both commit; an expired executor cannot renew or commit; accepted work, its source bytes and staged outputs survive restart/reclamation without any workflow row.

### Phase 2 — Implement the worker runtime module

- Build named worker registration, queue and timer wakeups, admission policies, bounded execution passes, resource registry and handles, next-action scheduling and graceful shutdown.
- Implement threshold, maximum wait, threshold OR maximum wait and scheduled flush using the semantics above. Keep grouping supplied by the feature module.
- Replace `RunOperations` selection/execution with the runtime. Convert every existing built-in and registered operation handler to the execution contract; retain one execution path. Preserve short transactional batches for database maintenance.
- Give long external work independent capacity so OCR/provider calls do not block field indexing, bulk updates or unrelated operation kinds.
- Implement fair bounded dispatch and acceptance quotas. Persist long backoffs without holding slots.
- Implement the defined pause/drain/unload/disable/flush behavior. Maintain polling, fencing and consumer cancellation semantics while admissions are paused.
- Move cleanup scheduling out of the removed loop into explicit maintenance registration so retention still runs even when queues are empty. Move activity record purge and result cache expiry out of `Runner.Purge` into the same registration.
- Expose queue depth/oldest age, next eligibility, active claims, retries, batch/drain progress and resource residency through existing logs/status conventions.

Exit: fake feature modules demonstrate policy and lifecycle combinations; long blocked external work does not block unrelated operations or hold the SQLite writer.

### Phase 3 — Adopt local engines, document text, image optimization and PDF composition

- Register every local engine (OCR, Laya, local embeddings) as a runtime resource. Delete their idle timers and the workflow `workGate` in the same change and route all current callers, including workflow actions and query-time embedding, through resource handles. Later phases convert callers to operations without touching residency again.
- Extract the document-text operation behavior from the script-engine branch into a feature module on `main`, including shared extraction settings and result reuse. Do not import the script runtime or generated SDK.
- Register `POST /v1/items/{id}/text` through module routes. Return `202`, the operation and its location; retain the ordinary text-read route for large results.
- Validate/capture source version and OCR settings at acceptance, protect source bytes, compute outside the writer and apply results under a valid claim. Recheck authority/freshness at the boundaries specified in Phase 0.
- Exercise on-demand, maximum idle and scheduled drain/warming residency with OCR.
- Make the existing JSON workflow's document-text action a consumer of the operation, using the existing extensible wait/completion facilities. Delete its direct extraction execution path and duplicated result persistence. Any required general completion binding is a consumer of the Phase 1 socket.
- Adopt the existing `file.optimize_image` and `file.compose_pdf` processing in document feature modules using the same OCR resource owner and worker execution policies. Replace direct interpreter execution and model access; preserve ordered photo selection, source-change guards, candidate/skipped results and explicit review/promotion. Do not introduce a second OCR gate.
- Register typed operation routes/contracts for the processing actions as needed for API consumers; represent ordered selections and primary-item identity explicitly rather than requiring a workflow run. Keep approval and guarded promotion as explicit consumer/domain actions authorized against the staged file's owner.
- Protect staged optimized images/PDFs through result consumption and review using the artifact ownership contract, including cancellation, failed guards, promotion and orphan recovery.

Exit: document processing works without workflow records; API and workflow callers use the same execution/lifecycle owner; changing a file during queued work cannot redirect processing to different bytes; existing optimization/composition retain guarded staging and review behavior without bypassing worker policy; every local model has one residency owner.

### Phase 4 — Move AI Batch ownership to its module

- Separate provider coordination and durable requests from `internal/workflowai` into an operation module independent of the workflow interpreter. Choose one module namespace and update all callers.
- Replace workflow waits as the authoritative provider input queue with operation-owned requests. Existing workflows submit operations and consume completions; no synthetic runs are created for API callers.
- Provide a bounded, typed feature API for accepted AI requests and results; update OpenAPI and authorization tests together. Credentials and provider selection stay under trusted deployment/module configuration.
- Replace the `ai.batch` built-in coordinator, its workspace startup requirement and workflow-only admission configuration with named worker configuration. Remove the replaced activity, parameter forms, sample usage and documentation in this cutover. `AI_BATCH_DEADLINE` becomes the default completion deadline of AI operations.
- Apply compatible grouping and threshold OR maximum wait. Keep provider-specific payload limits, immutable submission membership, idempotency/reconciliation, line validation and per-caller authorization in the AI module.
- Give retry ownership to the worker: under a worker, `internal/ai` clients make one attempt and report `Retry-After`. Share provider rate state across workers that use the same connection/credential.
- Preserve the distinction between reusable answer cache and authoritative accepted requests/provider results. Expiring a cache cannot lose accepted work or cause an ambiguous submission to be recreated.
- Update `ai.complete` and `search.enrich` consumers, including fan-out/join and partial success/retry behavior, to use module operations. Delete the direct provider execution paths they replace. Immediate admission uses the same execution machinery.
- Move shared admission policy ownership out of `internal/workflow`; update every caller instead of keeping a forwarding shim.

Exit: both plain API and current workflow callers use the same batch queue; threshold/time admission works across restarts; polling continues with an empty admission queue; ambiguous provider creates are never blindly repeated; provider retries stay within the operation budget.

### Phase 5 — Move local decisions onto operation workers

- Extract decision preparation, result validation, caching and guarded completion from `internal/workflow/decisions.go` into a feature module using `internal/decision`. Keep source/taxonomy semantics in the module, with no Laya-specific Core fields or scheduling branches.
- Define typed operation/API contracts for generic questions and managed-taxonomy decisions through module routes. Support inline text/JSON without an item, with an explicit authorized workspace context for cache scope, and document-backed requests over already extracted text with retained source references. Update OpenAPI, authorization tests and current decision docs together.
- Make current `ai.decide` and `taxonomy.decide` workflow actions consumers of these operations. API and workflow callers use the same queue, worker policies, model instance and completion logic. Delete their replaced direct inference paths and interpreter-owned model wiring; retain workflow routing/review as consumer behavior.
- Support immediate, scheduled and threshold OR maximum wait admission. Count logical ready decision requests after authorized deduplication; do not count expanded taxonomy candidates or question rows toward the queue threshold. Preserve separate caller completions and cache isolation.
- Both decision kinds share the Laya resource identity registered in Phase 3. The diagnostic CLI manages its own engine explicitly without creating application workflow records or a second idle policy.
- Preserve model/artifact and serializer identities, input hashes, deterministic ordering, supported question types, validation and existing row/token/output bounds. Pin the execution identity of accepted work; reject or explicitly defer unavailable artifacts instead of silently changing models. Queue admission must not change native batch limits or taxonomy probability thresholds.
- Reauthorize source and taxonomy access and check current source/fingerprint guards before publishing a successful result, including cache hits and delayed work. Keep guarded metadata writes as explicit domain actions. Decision operations return data/scores and never create terms, update metadata or create tasks implicitly.
- Keep reusable probability caches separate from authoritative operation completion. A failed guard or cancelled operation cannot publish success, although authorized inference may remain reusable under existing cache rules. Preserve `done`/`none`/error behavior in current workflow consumers.

Exit: API-only generic and taxonomy decisions work without workflow records; current workflows use the same implementation; threshold/time admission and restart recovery are covered; delayed/cached results cannot bypass source or taxonomy guards.

### Phase 6 — Adopt background and interactive embeddings

- Move immediate generation embeddings and deferred/recovery sweeps from `internal/workflow/embed.go` into an embedding feature module. Current workflows consume operation results; preserve generation ownership, publication guards, partial progress, vector reuse and model identity through typed boundaries.
- Replace the DBOS-owned embedding sweep schedule with explicit worker maintenance registration. Remove the replaced schedule and direct sweep execution in the same change; there must be one recovery owner before DBOS is removed on the script branch.
- Give interactive semantic/hybrid search a bounded latency/capacity policy on the shared embedding handle, independent of bulk batch thresholds. Bound background inference units so bulk work cannot monopolize inference. Define busy/deadline behavior explicitly in the API; do not silently change requested search mode.
- Use one local embedding model lifecycle and shared provider limits across query-time inference and background work. Keep synchronous inference outside database read/write transactions; do not create durable operations for ordinary search queries.
- Preserve bounded sweeps and unused-vector collection, and update semantic search and partial-recovery tests plus native unload/reload probes. Remove direct workflow model wiring.

Exit: immediate/deferred embedding generation and recovery use the worker foundation; interactive search stays usable during bulk processing; model residency and rate limits have one owner and recovery no longer depends on a workflow scheduler.

### Phase 7 — Complete lifecycle adoption and main validation

- Finish adoption for all consumers and maintenance tasks inventoried in Phase 0. Document which schedules remain workflow orchestration on `main` and which execute as worker maintenance; account for blob cleanup, operation/live/delta retention, activity record purge, result cache expiry, integration refresh and vector maintenance before script-branch scheduler removal.
- Remove replaced loops, engine timers, workflow coordinator configuration and provider-specific dependencies in Core. Update startup/shutdown composition, tests, contracts, samples and current-app docs together.
- Verify existing operation kinds, maintenance retention, file reclamation, decision quality/numerical fixtures and current workflow behavior after each cutover. Keep the JSON interpreter/DBOS as the current workflow consumer on `main`; their replacement is separate feature work.
- Run repository-required formatting, generation, vet/static analysis and CI checks appropriate to the changed packages, plus the acceptance matrix below. Use existing native-model fixtures/probes for lifecycle and resource checks.

Exit: main provides usable API operations and policy-driven workers independently of the script-engine branch, with one implementation of each replaced behavior.

## Apply to the script-engine branch

After Phases 1–7 land, merge updated `main` into the script-engine branch. Resolve conflicts around the resulting module/operation contracts, not by retaining both implementations.

1. Remove the branch's duplicate document-text registration, `API.DocumentText` dependency and direct model lifecycle wiring. Use the modules composed on `main`.
2. Preserve transactional in-process API execution, but use the same registered routes and request context as external API calls. A script-created operation and its replay step must commit atomically; rollback cannot leave orphan accepted work or source leases.
3. Remove workflow-specific `ReleaseWaits` SQL from the operations repository and both drivers. The script module consumes the generic completion boundary, owns its wait tables, and reconciles recorded waits on resume. Completion before wait registration and missed notifications must both work.
4. Keep the script scheduler responsible for script slices, replay, cancellation and run retention. Worker admission, resource lifetime, provider polling and operation execution remain in the worker/module layer. Suspending a script does not unload another operation's active resources.
5. Regenerate the Kiota client and embedded bundle from the resulting OpenAPI contract. Check exclusion scope, SDK type checking, deterministic generation and bundle tests.
6. Extend script integration tests: submit document text, optimization/composition, embedding, LLM and generic/taxonomy decision operations, suspend, restart, let a worker finish, replay and consume results without repeating submission. Include rejected requests, independent cancellation of shared work, source/output retention and changed-source/taxonomy guards. Scripts use module routes through the generated SDK; do not add script-only inference helpers. Script cancellation cancels the script's own operations, which never cancels computation shared with other operations.
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
| Concurrent API retries / lost response / key payload conflict | Matching caller-scoped keys return one accepted operation; changed payload conflicts; authorization and retention remain enforced |
| Idempotency key after operation retention | The expired binding no longer matches; the same key creates a new operation |
| Overload at acceptance | Rejected with `429` and `Retry-After` before anything is persisted; accepted work stays durable |
| Competing workers / expired lease | At most one valid claim can apply a result; stale attempts cannot commit or renew |
| Provider create with uncertain response | Reconcile the fixed intent; never infer permission to create again from an inconclusive lookup |
| Revoked caller / cancelled or superseded work | Enforce the reviewed authority and terminal-state rules; avoid stale publication and release resources safely |
| Source replaced / history collected while queued | Process the accepted bytes; retained sources survive until the appropriate release boundary |
| Idle unload / scheduled warm / active inference | One resource owner; no unload during use; equivalent inference after reload |
| Decision threshold/time admission | Count logical requests independently of native row batching and taxonomy probability thresholds; retain deadlines across restarts |
| Generic and taxonomy decisions through API/workflows | Shared operation execution and Laya lifecycle; preserve question/candidate order, numeric fixtures, limits and consumer routing |
| Decision source or taxonomy changes while queued / cached | Reauthorize and fail guarded publication; no implicit item mutation or successful stale completion |
| Long OCR versus short maintenance | Independent capacity; short work and database writes continue |
| Image/PDF processing with OCR worker capacity one | Uses the OCR handle in-process; no child operation, self-deadlock or second gate |
| Busy workspace / interactive stream / shared provider limit | Bounded fair progress across tenants and groups; overload rejection precedes acceptance; rate limits apply across workers |
| Bulk embedding versus interactive search | Query inference has bounded capacity/latency policy; batch thresholds and sweeps cannot monopolize its shared resource |
| Maximum wait reached / closed window / retry-not-before / completion deadline | Each clock has independent persisted semantics; dispatch reports its blocking reason and does not hot-loop |
| Completion deadline while parked in a provider batch | The member fails with the deadline code; the batch and its other members continue |
| HTTP retries inside repeated worker execution | Total attempts and elapsed retry time stay within the operation budget; terminal failures do not block other work |
| Pause / drain / unload / disable / flush | Documented control semantics; required polling/renewal continues and active native resources are retained until safe release |
| One shared consumer cancelled / others remain | Cancelled consumer cannot publish success; remaining authorized consumers still complete; shared provider work is not cancelled indiscriminately |
| Staged PDF/image written before crash / retained for review | Provisional/result references protect bytes; promotion is guarded; only unreferenced stale outputs are reclaimed |
| Graceful shutdown and abrupt restart | No new claims during drain; unfinished work remains recoverable; resources close safely |
| API versus current workflow versus later script | Same operation implementation, admission policy, resource owner and completion semantics |

## Deferred work

- Separate worker executables, distributed transport, remote blob access and multi-server deployment/capacity enforcement.
- Process-wide CPU/memory budgets: per-resource cost estimates, reservations before load, and aggregate limits across workers.
- Durable child-operation dependencies between operations.
- Worker administration UI, public configuration CRUD and arbitrary untrusted worker code.
- New operation families beyond the existing document, embedding, LLM and decision processing in scope. General approval operations are deferred; preserve existing review/task consumers and their artifact references during processing cutovers.
- The script engine's own lifecycle, trigger system, scheduler and replacement of JSON workflows/DBOS.

These follow after the single-process operation/worker contract is proven. Do not add feature-specific base tables or speculative distributed infrastructure to anticipate them.
