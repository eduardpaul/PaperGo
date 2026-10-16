# Workflow extensions

Register compiled actions with `workflow.RegisterActivity(ActivitySpec)` and compose them with ordinary workflow definitions. `internal/workflowai` is an example: it owns the AI protocols, request hashes, admission coordination and provider responses. The runner imports no AI module. The executable composes it and supplies its runtime through `workflowai.Configure`; other modules can use `Tools.Extensions` without adding provider fields to the interpreter.

An action's lifecycle has four boundaries:

1. `Prepare` resolves tokens and authorizes input inside the DMS step transaction. Return an output, a `Job`, or a `Park`.
2. `Work` performs network or expensive work outside the database writer. DBOS checkpoints its result. Work may replay after a crash, so external writes need a durable intent, reconciliation or an idempotency key. It must not write domain data.
3. `Commit` validates and applies work results inside another DMS step transaction.
4. `Resume` receives a `Completion` after a wait. It runs transactionally as the workflow version's author and can recheck authority, return output and routing, or request another work/wait phase. Without a handler, completion output/port passes through and completion errors follow normal node retry/error handling.

The engine executes work and wait transitions uniformly. The execution ID is deterministic for a particular node invocation and attempt; replay preserves it, while another visit gets a new identity. Every parked invocation belongs to a real run. A human-task resolver and an AI provider coordinator both complete the same wait abstraction. `RegisterWaitResolver` adapts another transactional business source to it without modifying the runner.

Use `Tools.Records.WithTx(step.DMS().SQLTx())` for durable extension state. Records have namespace, scope, key, opaque JSON, state and optional expiry. `PutImmutable` detects accidental identity reuse with a different payload. `Reference` retains a record for a wait, submission or run; `Release` drops an owner's references. References must be released on completion or cancellation. Run-owned references are released automatically when that run terminates. `Park.PayloadID` handles wait retention automatically.

Use `Claim`, `CheckLease` and `ReleaseLease` for one executor of shared state. Claims expire, terminal run holders can be replaced, and the fencing token must be checked in the commit transaction before applying results. External provider idempotency is still required: a local lease cannot make a remote API call exactly once.

`Prepared.BlobIDs` protects source bytes while immediate work runs; `Park.BlobIDs` protects them through a durable wait. The engine releases these leases on consumption or termination. Reclamation and recycle-bin collection consult leases directly. Providers do not encode ownership or byte protection in arbitrary JSON properties.

`Tools.Results` is an optional reuse cache, independent of execution durability. Losing or expiring a cached answer must not lose an accepted request or its completed response. Keep accepted requests, in-flight membership and authoritative results in activity records until consumers have recorded their completions.

Admission is extensible through `RegisterAdmissionPolicy(name, fn)`. A policy selects group sizes from ready count, configured maximum and whether the cron flush is due; `workflow.Admit` validates those sizes. The built-in policies are `threshold`, `schedule` and `both`. An action coordinator owns compatibility grouping, payload limits, its clock/schedule and provider protocol. Immediate execution bypasses admission. Another compiled module can register its own policy and action/coordinator without changing the runner or interpreter.

The AI coordinator submits at most one new group per pass and polls/reconciles at most one existing group each. Durable drain state carries a scheduled backlog across passes. Queue, connection and effective model scope all in-flight records. An atomic intent transition authorizes at most one create across concurrent replays. Provider metadata identifies fixed submission membership with an identity unique to its admission; a lookup that cannot establish absence never authorizes resubmitting an ambiguous create. Missing confirmation leaves the consumer waiting until confirmation or its deadline.
