# Tasks and notifications (plan)

Status: **proposed.** Nothing in this plan is implemented yet. It designs two core building blocks that workflows and the rest of PaperGo share:

- **Tasks:** *action required.* Someone has to do something and say so: approve, reject, fill in a form, confirm. A workflow can wait for the answer.
- **Notifications:** *something happened.* People are told; nobody has to answer, and nothing waits.

**An approval is a task** whose outcomes are approved and rejected, with an optional comment, form and review. There is one model, one inbox and one API for every kind of action request.

User story: *as a developer or power user, I can create a custom task or send a notification at any point of my workflow.* Other parts of PaperGo (the runner, sharing, publishing) use the same blocks through a Go API, so a person sees every request and every message in one place, whatever raised it.

PaperDotNet was reviewed as a reference (Tasks, Notifications and Workflows modules, ADR-0016, ADR-0036, features TSK, NTF, EVT). It keeps approvals in a workflow table of their own, separate from tasks. Its tasks are list items in a to-do list, and nothing links the two. PaperGo ports its notification contract (types, per-user dedup, channel settings, signed webhooks with backoff) and does not port its split (see [compared with PaperDotNet](#compared-with-paperdotnet)).

## What PaperGo has today

- **Approvals.** The `approval` activity writes an `approval_requests` row (assignees, title, form, review, status, decision) and parks the run until someone decides (`internal/workflow/approvals.go`, `internal/runner/approvals.go`). `GET /v1/me/approvals`, `POST /v1/approvals/{id}/decision` and the web Approvals screen serve it.
- **Waiting.** A parked run holds no worker. It shows as `waiting`, counts as active for concurrency, and is resumed by the dispatcher once the request is decided. Cancelling the run closes the request.
- **Live updates.** `GET /v1/events` streams `item`, `operation` and `run` frames from the `live_changes` journal.
- **No notifications.** Nothing tells a person that a request is waiting, that a run failed, or that a document changed.

This plan turns `approval_requests` into tasks, adds notifications next to them, and replaces the approvals API and screen. PaperGo has no released data, so the table and routes change in place.

## Principles

1. **One record per kind of thing.** Every action request is a task, and every message is a notification, whether it comes from a workflow, a person or the system. No module keeps its own inbox.
2. **Blocks, not processes.** Tasks and notifications only record, deliver and enforce. Who is asked, when and what follows is a workflow's job, so people can see and change it. Built-in workflows use the same activities as everyone else.
3. **Core guarantees stay in code.** Only assignees complete a task; a task's form is checked; nobody is told about content they cannot read; a waiting run resumes exactly once. Workflows cannot turn these off.
4. **Safe to repeat.** Every write is part of a step transaction or carries a deduplication key, so a replayed step or a retried job creates nothing twice.

## Tasks

### Model

A task belongs to a workspace and may concern one item.

| Field | Meaning |
| --- | --- |
| `id`, `workspace_id`, `item_id` (optional) | where the task belongs |
| `kind` | `task` or `approval`. Only changes presentation and defaults; the same rules apply |
| `key` (optional) | a lowercase name the creator gives it, such as `legal_review`, so workflows can filter `task.*` events and people can filter lists |
| `title`, `description` | plain text; up to 500 and 4,000 characters |
| `assignees` | entries, resolved when the task is created (see [recipients](#recipients)) |
| `completion` | `any` (the first response completes it, the default) or `all` (every assignee responds) |
| `outcomes` | the answers, in order: `[{key, label, style, comment, decisive}]`. Default `[{key: "done", label: "Done"}]` |
| `input_schema`, `review` | an optional [form](../workflows.md#forms) answered with the response, and an optional staged-file review (`file.compare`, `file.preview`) |
| `priority`, `due_at` | `low`/`normal`/`high`; an optional due time |
| `reminders`, `on_due` | when to remind assignees before or after the due time, and whether the task `expires` at the due time or stays open (`keep`) |
| `source` | `workflow` (with `workflow_id`, `run_id`, `node`), `user` or `system` |
| `wait` | whether a run waits for it |
| `status` | `open`, `completed`, `cancelled` or `expired` |
| `outcome`, `completed_by`, `completed_at`, `comment`, `input` | the result |
| `created_by`, `created_at`, `updated_at`, `version` | ownership and the ETag |

`task_responses` keeps each response (`subject`, the group it answered for, `outcome`, `comment`, `input`, time). In `any` mode there is one; in `all` mode there is one per assignee.

An outcome:

- `key` and `label`: `approved` / "Approve".
- `style`: `primary`, `danger` or `default`, for the button.
- `comment`: `none`, `optional` (default) or `required`. A rejection usually requires one.
- `decisive` (`all` mode only): choosing it completes the task at once with that outcome, such as a rejection in an all-must-approve review.

### Lifecycle

```
open ──respond──▶ completed      (outcome chosen)
  │──due, on_due=expire──▶ expired    (outcome "expired")
  └──cancel──▶ cancelled         (run cancelled or replaced, creator or manager cancelled it)
```

- **`any`:** the first valid response completes the task. Later responses get 409.
- **`all`:** each assignee responds once; a group entry counts as one assignee, answered by any of its members. A decisive outcome completes the task at once. Otherwise it completes when every assignee has responded, with the outcome chosen most often, ties going to the outcome listed first. Every response stays in the task's output, so a workflow can route on them itself.
- Completed, cancelled and expired tasks never change again. A response with invalid form values leaves the task open.

### Recipients

Tasks and notifications accept the same entries:

| Entry | Means |
| --- | --- |
| `<subject>` | a user |
| `group:<id>` | a group, nested groups included (`GroupClosures`) |
| `role:owners`, `role:members` | the workspace's owners or members |
| `creator`, `actor` | the item's creator; the person who caused the run |
| `field:<key>` | the people in a person field of the item |

Tokens work too (`{item:values.reviewer}`). An empty list means the workspace's owners. Unknown entries fail the node with their names, rather than being dropped as PaperDotNet does: a task nobody can see is a silent failure. Inactive users are left out; a task with nobody left fails.

### Access

- **Seeing a task:** its assignees (members of an assigned group included), its creator, and the workspace's owners. When it concerns an item, an assignee must also be able to read the item's draft. This is checked when the task is created (the node fails, naming who cannot) and again when someone responds.
- **Responding:** only assignees. Owners can reassign, not respond for others.
- **Changing:** the creator and the workspace's owners can change the title, due time and assignees of an open task (with `If-Match`), and cancel it. A workflow's task belongs to its run: cancel the run instead, or let a workflow cancel it with `task.cancel`.
- **Reassigning:** an assignee can hand their part to someone else when the task allows it (`allow_reassign`). Owners always can. The new assignee needs the same access.

### Waiting

`wait: true` (the default in workflows) parks the run exactly as approvals do today: the run holds no worker, shows as `waiting`, and the dispatcher resumes it once the task is completed or expired. The node then follows the port named after the outcome (`approved`, `expired`…), and its output is the result:

```json
{"task_id": "…", "outcome": "approved", "completed_by": "alice", "comment": "…", "input": {…}, "responses": [{"subject": "alice", "outcome": "approved", …}]}
```

`wait: false` creates the task and continues at `done` with `task_id`. Another workflow can react to `task.completed` later. Use it for follow-ups that should not hold the process, such as "file the signed copy".

Cancelling or replacing a waiting run cancels its open tasks; a run never waits on a task that can no longer be answered.

### Ports from outcomes

Today each activity has fixed ports. A task node's ports are its outcomes, plus `expired` when it can expire, plus `done` and `error`. The engine needs one change: an activity may declare that its ports come from an input (`ports_from: "outcomes"`). Saving a workflow then checks `next` against the node's own outcomes, and the catalog tells the editor to draw one port per outcome.

### Due times, reminders and expiry

- `due` is a duration (`72h`) or a date/time, as in `delay`.
- `reminders` are offsets from the due time, such as `["-24h", "0s", "48h"]`, at most five. Each sends a reminder [notification](#notifications) to assignees who have not responded, with deduplication key `task:{id}:reminder:{offset}`.
- `on_due: expire` completes the task as `expired` at the due time, and a waiting run follows `expired`. Escalation is then a workflow: from `expired`, ask a manager with another task, reassign, or notify.
- The runner's dispatch loop runs a pass each minute over open tasks whose next reminder or due time has passed (`tasks_due` index on the next time). No job runs per task.

### Events

Task changes are domain events, so workflows can react to them (`task.*` triggers, with a `key` filter):

| Event | Data |
| --- | --- |
| `task.created` | `task_id`, `key`, `kind`, `assignees`, `item_id` |
| `task.completed` | the above plus `outcome`, `completed_by` |
| `task.expired`, `task.cancelled` | the above |

Events of a workflow's own task are one level deeper than the run, which keeps loop protection working.

### Workflow activities

| Activity | Kind | Does |
| --- | --- | --- |
| `task` | flow | creates a task; with `wait`, follows the port of its outcome |
| `approval` | flow | a `task` with `kind: approval` and outcomes `approved` (Approve, primary) and `rejected` (Reject, danger, comment required). Takes every other `task` input |
| `task.cancel` | action | cancels an open task (`task_id`), such as a standalone follow-up that became pointless |

A custom task with three answers and a deadline:

```json
"check": {"activity": "task", "inputs": {
  "title": "Check the invoice data of {item:name}",
  "assignees": ["role:members"],
  "outcomes": [
    {"key": "correct", "label": "Correct", "style": "primary"},
    {"key": "fix", "label": "Needs a fix", "comment": "required"},
    {"key": "not_invoice", "label": "Not an invoice", "style": "danger"}],
  "input_schema": {"type": "object", "properties": {"total": {"type": "number", "title": "Total"}}},
  "due": "48h", "reminders": ["-4h"], "on_due": "expire"},
  "next": {"correct": "save", "fix": "notify_uploader", "not_invoice": "move", "expired": "escalate"}}
```

An all-must-approve approval:

```json
"legal": {"activity": "approval", "inputs": {
  "title": "Approve publishing {item:name}", "assignees": ["group:legal", "{item:values.owner}"],
  "completion": "all", "decisive": ["rejected"], "due": "5d", "reminders": ["-1d"]},
  "next": {"approved": "publish", "rejected": "tell_author"}}
```

### Standalone tasks

People and integrations create tasks too, with no workflow: "please review this contract by Friday". `POST /v1/workspaces/{id}/tasks` takes the same inputs as the activity (resolved recipients only, no tokens). The creator needs read access to the workspace, and write access when the task concerns an item. Workflows react to them through `task.*` triggers.

A standalone task is not a to-do list. PaperDotNet's TSK features (boards, subtasks, checklists, recurrence) are content that people manage. They belong in a "Tasks" list template built on items, which can raise tasks through workflows when it needs action from someone. That stays outside this plan.

### API

| Route | Does |
| --- | --- |
| `GET /v1/me/tasks?status=&kind=&key=&workspace_id=&item_id=&due_before=` | the caller's tasks: assigned to them or their groups; paged, open ones by due time |
| `GET /v1/workspaces/{id}/tasks` | every task of a workspace (owners) |
| `GET /v1/items/{id}/tasks` | open and recent tasks about an item that the caller may see |
| `POST /v1/workspaces/{id}/tasks` | creates a standalone task |
| `GET /v1/tasks/{id}` | a task with its responses |
| `PATCH /v1/tasks/{id}` | title, description, due time, reminders and assignees (`If-Match`) |
| `POST /v1/tasks/{id}/responses` | `{outcome, comment, input}` |
| `POST /v1/tasks/{id}/reassign` | `{from, to}` |
| `POST /v1/tasks/{id}/cancel` | cancels a standalone task |
| `GET /v1/staged-files/{id}`, `/content` | unchanged: the staged file a review shows |

The approval routes go away: an approval is `GET /v1/me/tasks?kind=approval` and `POST /v1/tasks/{id}/responses`.

## Notifications

### Model

A notification is one message to one person.

| Field | Meaning |
| --- | --- |
| `id`, `recipient`, `workspace_id` (optional) | who and where |
| `type` | `task.assigned`, `task.reminder`, `task.expired`, `task.completed`, `workflow`, `workflow.failed`, `system`, … |
| `title`, `body` | plain text; up to 300 and 4,000 characters. Rendered as text, never as HTML |
| `link` | `{item_id}`, `{task_id}` or `{run_id}`, opened in the app |
| `source` | `workflow` (`workflow_id`, `run_id`, `node`) or `system` |
| `dedup_key` | unique per recipient when set: a second send with the same key creates nothing |
| `created_at`, `read_at` | read state |

A notification never starts a workflow and raises no domain event, so notifications cannot loop.

### Rules

- **Access at send time.** A notification that links an item, task or run goes only to recipients who can read it now. The others are skipped and counted in the result. Their titles can contain item data through tokens, so they are skipped, not sent without the link.
- **Nobody is told about their own action.** The actor of a change is left out of notifications about it, unless a workflow names them explicitly.
- **Bounded fan-out.** At most 500 recipients after expanding groups and roles; the send fails above that.
- **Deduplication.** Workflow sends default to a key derived from the run and node, so a replayed step does not send twice. Callers can pass their own, such as `invoice:{item:id}:overdue`, to send once ever.

### Channels and settings

- **Inbox** (in the app) and **live** (a `notification` frame on `GET /v1/events`): always on. A notification is written to the inbox in the transaction that sends it.
- **Email** (SMTP, off unless configured) and **webhook** (one signed URL per user): delivered after commit by a delivery queue: `notification_deliveries` with `status`, `attempts`, `next_attempt_at` and `last_error`. Retries back off 1 min, 5 min, 30 min, 2 h, then 12 h, up to six attempts, as in PaperDotNet. Webhooks are https only, to public addresses only (checked at connect time), signed with HMAC-SHA256 over `"{timestamp}.{body}"`.
- **Settings per user and type:** which external channels each type uses (email, webhook), quiet hours that only delay external channels, and a daily digest instead of one email each. Organization defaults first, then personal overrides, as with preferences.

### Workflow activity

| Activity | Kind | Does |
| --- | --- | --- |
| `notify` | action | sends `title` and `message` to `recipients`, linked to the run's item unless `link` says otherwise |

```json
"tell_author": {"activity": "notify", "inputs": {
  "recipients": ["creator"],
  "title": "{item:name} was not approved",
  "message": "{step:legal.completed_by}: {step:legal.comment}"}}
```

Inputs: `recipients`, `title`, `message`, `link` (`item_id` or `task_id`; default the run's item), `type` (default `workflow`), `dedup_key`. Output: `sent`, `skipped` (no access), `deduplicated`.

### System notifications

The core sends these itself, because they are part of what tasks and runs guarantee. People control their channels in settings; the inbox always gets them.

| Type | Sent when | To |
| --- | --- | --- |
| `task.assigned` | a task is created, or someone is added or reassigned | the new assignees |
| `task.reminder` | a reminder offset passes | assignees who have not responded |
| `task.expired` | a task expires | its assignees and creator |
| `task.completed` | a standalone task is completed | its creator |
| `workflow.failed` | a run fails | the person who saved the workflow version; the workspace's owners for built-ins |

Anything else a process wants to tell people, such as "your document was published" or "a follow-up is overdue", is a `notify` node in a workflow people can see and change.

### API

| Route | Does |
| --- | --- |
| `GET /v1/me/notifications?unread=&type=&before=` | the caller's inbox, newest first |
| `GET /v1/me/notifications/unread-count` | the badge |
| `POST /v1/me/notifications/read` | `{ids}` or `{before}`: marks them read |
| `DELETE /v1/me/notifications/{id}` | removes one |
| `GET`, `PATCH /v1/identity/me/notification-settings` | channels per type, quiet hours, digest, webhook URL (`If-Match`) |
| `GET`, `PATCH /v1/organization/notification-settings` | organization defaults (`organization.manage`) |

## Go building blocks

Modules use the blocks through two small packages, inside the caller's write transaction:

```go
// internal/tasks
func Create(ctx context.Context, t *dms.Service, by string, in tasks.Input) (*tasks.Task, error)
func Respond(ctx context.Context, t *dms.Service, subject, id string, r tasks.Response) (*tasks.Task, error)
func Cancel(ctx context.Context, t *dms.Service, by, id, reason string) error

// internal/notify
func Send(ctx context.Context, t *dms.Service, m notify.Message, to []string) (notify.Result, error)
```

- `tasks` writes the task, its `task.*` event and its `task.assigned` notifications in the caller's transaction. `notify` writes inbox rows and queues external deliveries in the same way. Both also enqueue `live_changes` rows (kinds `task` and `notification`), which only reach the recipient.
- `internal/workflow` keeps only the activities: `task`, `approval`, `task.cancel` and `notify` call these packages. `runner.Await` waits on a task instead of an approval request; nothing else in the runner changes.
- Recipient resolution lives in one place (`tasks.Recipients`), used by both.
- The delivery queue and the due-time pass run in the runner's dispatch loop, which already resumes parked runs.

## Web app

- **Inbox** replaces the Approvals screen. Two tabs: *Tasks* (waiting for me, by due time, overdue first, filters by kind and workspace) and *Notifications* (unread first; mark read, mark all read).
- **Bell** in the top bar with the unread count, updated by `notification` frames.
- **Task detail**, one renderer for every kind: title, description, linked item, due time, the review, the form, then one button per outcome in its style. A required comment is asked for before submitting. Responses show below, so all-mode progress is visible.
- **Item page**: a Tasks panel lists the item's open tasks.
- **Workflow editor**: a task node edits its outcomes (key, label, style, comment, decisive), and the canvas draws one port per outcome. The recipients picker offers people, groups, roles and the item's person fields.
- **Settings**: a notifications section with channels per type, quiet hours and digest.

## Compared with PaperDotNet

| | PaperDotNet | PaperGo |
| --- | --- | --- |
| Approvals | own table and inbox in the workflow engine | a task of kind `approval` |
| Custom action requests | planned ("input requests", EVT-13) | `task` with any outcomes and a form |
| Completion | first decides; unanimity needs chained nodes | `any` or `all`, with decisive outcomes |
| Timeouts | one escalation adds assignees; no timeout outcome | reminders, `expired` port; escalation is a workflow |
| Assignees | users, `group:`, `field:`, `creator`, `actor`; unknown names dropped | the same plus `role:`; unknown entries fail the node |
| To-do tasks | list items (status, board, checklist, recurrence) | outside this plan: a list template, which can raise tasks |
| "Task assigned" notification | not sent | sent by the core |
| Notifications | inbox, settings per type, webhooks with backoff, quiet hours, daily digest | the same contract, plus email |
| Workflow `notify` | people, title, body, linked to the run's item | the same, plus link, type, dedup key, access check per recipient |

## Phases

Each phase ships with its tests, OpenAPI contract, docs and web screens, and edits `migrations/20261007000100_schema.sql` in place.

1. **Tasks core.**
   - Replace `approval_requests` with `tasks` and `task_responses`; `internal/tasks` with recipients, access checks and `any`/`all` completion.
   - Ports from inputs in the engine.
   - The `task`, `approval` and `task.cancel` activities; `wait: false`; `task.*` events and triggers.
   - The task API, standalone tasks, and the Inbox *Tasks* tab with the generic task detail. The built-ins `documents.optimize` and `documents.photos_to_pdf` keep working through `approval`.
2. **Notifications core.**
   - `notifications` and `internal/notify`; the `notify` activity; the inbox API, live frames, bell and *Notifications* tab.
   - System notifications for tasks and failed runs; the item Tasks panel.
3. **Due times.** `due`, `reminders` and `on_due` with the dispatch-loop pass, the `expired` port, reassignment, and editing open tasks.
4. **External channels.** Notification settings; the delivery queue; webhooks; email through a maintained SMTP library such as `github.com/wneessen/go-mail` (MIT), off unless configured; quiet hours and daily digests.

Follows and change subscriptions (PaperDotNet NTF-03, API-06) build on phase 4's delivery queue and are planned separately.

## Decisions

The plan assumes these choices. Each can still change before phase 1.

1. **Tasks are core records, not list items.** Their guarantees (only assignees respond, forms are checked, runs resume once) cannot hold for content people edit freely. To-do lists stay a later list template.
2. **Approvals become tasks in place.** `approval_requests`, `/v1/me/approvals`, `/v1/approvals/{id}/decision` and the Approvals screen are replaced in phase 1, with no compatibility routes.
3. **The core sends task and run-failure notifications**, rather than a built-in workflow that a workspace could turn off and so leave people unaware of their tasks.
4. **Unknown recipients fail the node**, where PaperDotNet drops them silently.
5. **Email arrives in phase 4**, after webhooks, through a library rather than hand-written SMTP.
