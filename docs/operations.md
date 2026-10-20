# Operation lifecycle

Every piece of background work is an **operation**: index builds, bulk edits, OCR, previews, AI requests, search content, workflow runs and the tasks people answer. This page is the contract all of them share. Handlers, workers and workflows build on it rather than defining their own states.

## States

An operation is `pending` until it ends as `succeeded`, `failed`, `superseded` or `cancelled`. An ended operation never changes again. While pending, its `waiting` field says why:

| `waiting` | Meaning | Stored as |
| --- | --- | --- |
| `queued` | ready for its worker | no claim, not parked, `attempt_at` passed |
| `running` | a worker executes it | a claim whose `claim_until` has not passed |
| `retry` | deferred after a failed attempt | `attempt_at` in the future, `attempts` above 0 |
| `scheduled` | deferred without a failure, such as a sleeping workflow | `attempt_at` in the future, no attempt yet |
| `remote` | parked: something outside a worker ends it | `execution_id` set |

## Transitions

| Event | From | To | Attempts | Effects |
| --- | --- | --- | --- | --- |
| Accept | — | `queued`, or `remote` for an external kind such as a task | 0 | Commits with the request that created it. An idempotency key returns the operation it already names. The creating operation, if any, becomes its parent. |
| Claim | `queued`, or `running` whose claim expired | `running` | +1 | A claim lasts two minutes and is renewed every 40 seconds while the execution runs. An operation claimed after its attempts ran out (5 by default) fails with `retries_exhausted` instead. |
| Claim expires | `running` | `queued` | kept | The process stopped or lost the database. The next claim counts another attempt. The old execution's writes are fenced: it can neither save progress nor end the operation. |
| Progress | `running` | `running` | kept | A checkpoint under the claim; durable progress lets the next attempt continue. |
| Finish | `running` | `succeeded`, `failed` or `superseded` | kept | Under the claim, in one transaction with the handler's own database effects. |
| Error | `running` | `failed` | kept | The handler's failure cleanup runs in the same transaction. |
| Retryable error (`dms.Retry`) | `running` | `retry` | kept | The claim is released and `attempt_at` set; the error is kept on the operation. No worker waits during the delay. |
| Shutdown | `running` | `queued` | given back | Work cancelled by a draining worker releases its claim without counting the attempt. |
| Release or sleep | `running` | `queued` or `scheduled` | given back | An execution that made progress gives its attempt back, such as a workflow run between slices. |
| Park | `running` | `remote` | kept, or given back | The operation joins a durable module execution, such as a provider batch or a workflow wait, and its claim is released. Submissions keep the attempt; a workflow pausing after progress gives it back. |
| Member finishes | `remote` | ended | kept | The execution ends a member it still holds, such as a batch line's answer. A member that already ended or left is not touched. |
| Unpark | `remote` | `queued` or `retry` | kept | The execution gives members back, optionally with a delay and the reason. |
| Deadline passes | pending | `failed` (`deadline_exceeded`) | kept | Maintenance ends it, with the handler's failure cleanup. |
| Supersede | pending | `superseded` | kept | Newer work of the same kind made it unnecessary, such as a newer index build. |
| Cancel | pending | `cancelled` | kept | Through `POST /v1/operations/{id}/cancel`, or by PaperGo (a replaced or cancelled workflow run, a deleted item or workflow). The handler's cancellation cleanup runs, then every pending operation it owns is cancelled the same way. |

**Completion notification.** A write that ended operations calls the completion hook with their IDs once it commits, and workflow runs waiting for them resume. The hook is a prompt, not a guarantee: a process can stop between the commit and the call, so maintenance also reconciles waits from the operations' stored state.

**Retention.** Ended operations and their results are deleted 30 days after their last update. Kinds whose module keeps a record, such as workflow runs, are deleted under that module's retention instead. Pending operations are never deleted by age; their deadline ends them.

## Ownership and dependencies

An operation **owns** the operations created on its behalf: those a workflow run's requests start, and those a handler starts while it executes. The owner is the operation's `parent_id`. Ownership is fixed at creation. An operation that a request finds rather than creates keeps its owner, for example one an idempotency key replays.

- **Cancellation follows ownership.** Cancelling an operation cancels the pending operations it owns, and theirs in turn.
- **Waiting is a dependency, not ownership.** A workflow that waits for an operation resumes when it ends. Cancelling the workflow leaves that operation alone, because another run, a person or a module may depend on it too.
- **Success and failure do not cascade.** Work a finished run started goes on, such as the tasks it leaves for people.
- **Retention clears the link.** When an owner is deleted, its operations keep running without a parent.

## Durability

An operation's state, progress and result commit with the database effects of the step that produced them. Work outside the database (providers, object storage, the search store) runs at least once, under each module's [recovery protocol](architecture.md#durability-boundaries).
