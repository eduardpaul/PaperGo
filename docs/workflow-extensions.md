# Workflow extensions

Compiled features expose authenticated routes through `httpapi.Module` and describe them in `api/openapi.json`. The executable registers each module and its operation handlers before starting the API and workers. `internal/aiops`, `internal/documenttext` and `internal/searchcontent` provide examples.

A module handler uses `httpapi.Request.DMS` and `Subject` for its calls. Workflow requests use the same handlers in-process, bound to the transaction that records the script step. Modules must use that supplied service so their writes commit with the recorded response. `Request.Run` identifies the workflow run for features such as staged files and tasks.

Long work starts with `Service.EnqueueOperation` and returns `httpapi.Accepted`. An `OperationHandler` declares its scope, validates the payload transactionally, and executes either a bounded transactional `Step`, an `Execute` outside the writer, or an `ExecuteGroup` for grouped admission. `SaveOperation` commits progress and results under the operation's claim. Source files can be retained with `LeaseOperationBlobs` until the operation ends.

An execution can park its operations in a durable module execution with `ParkOperations`. A provider poller later calls `FinishParkedOperation` or returns members to the queue with `UnparkOperations`. External operations, such as task responses, end with their domain action instead of a worker. Failure and cancellation handlers run in the transaction that ends the operation.

Use `activitystore.Store` for module-owned durable records: namespace, scope, key, JSON payload, state and expiry. Bind writes to `DMS.SQLTx()` when they must commit with a domain change. Typed record and operation owners can retain records independently of cache expiry. Shared records support leases; commits must check the fencing token. External writes still need a durable intent, reconciliation or provider idempotency.

`resultcache.Cache` stores reusable answers separately from execution state. Losing a cache entry must not lose an accepted request. Keep in-flight membership and authoritative results durable until consumers have recorded their completions.

Named workers in `internal/worker` own operation kinds and configure concurrency, execution windows and immediate or grouped admission. Group policies use distinct inputs, thresholds, maximum waits and optional cron flushes. The feature owns provider-specific grouping and protocol details; workers own scheduling and execution claims.

Workflow scripts call these routes through `ctx.api` and await operations with `ctx.wait`. Regenerate the TypeScript client and embedded SDK after changing their API contract; see [sdk/README.md](../sdk/README.md). Features require no new interpreter activities or provider fields in the script engine.
