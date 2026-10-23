# Workflows

Workflows are reusable features that people run **on demand**, **after a change**, or **on a schedule**. A workflow is a TypeScript script that workspace managers can read, change, turn off or replace. That includes the processes PaperGo ships, which are *built-in workflows* rather than hidden code.

A workflow's script has a default export, `workflow({...})`, which declares:

- **triggers**: what starts a run, such as an item being created, a cron schedule, a manual start, or another workflow ending;
- an optional **condition**: a query filter the run's item must meet;
- optional **forms**: the launch form of manual starts (`input_schema`) and what a completed run returns (`output_schema`);
- **concurrency**: what a start does while another run on the same item is active;
- **run**: what the workflow does, through the PaperGo API.

Every save that changes the script or its parameters creates a new immutable **version**. Saving bundles the script and reads its declarations; a script that does not compile, or that declares something invalid, is refused with the reason. A run always executes the version it started with.

## Example

```ts
import { plain, workflow } from "@papergo/workflow";

export default workflow<{ reviewer: string }>({
  triggers: [
    { type: "item.created", collection_id: "<contracts list>" },
    { type: "manual", collection_id: "<contracts list>", selection: "per_item" },
  ],
  condition: { field: "status", op: "eq", value: "new" },
  input_schema: {
    type: "object",
    properties: { reviewer: { type: "string", title: "Reviewer", "x-papergo": { kind: "people", access: "write" } } },
  },
  async run(ctx, input) {
    const item = await ctx.api.v1.resources.byId(ctx.itemId!).get();
    const task = await ctx.api.v1.workspaces.byId(ctx.run.workspace_id).tasks.post({
      kind: "approval",
      itemId: ctx.itemId,
      title: `Review ${item!.name}`,
      assignees: input?.reviewer ? [input.reviewer] : undefined,
    });
    const answer = plain((await ctx.wait(task!.operationId!)).result) as { outcome: string };
    ctx.log("review", answer.outcome);
    return { approved: answer.outcome === "approved" };
  },
});
```

## Scripts

A script imports `@papergo/workflow`, the workflow SDK, and may be split into its own functions, but not into other files. It runs in an embedded JavaScript engine without network or file access. Its only way to act is `ctx.api`, a client of the [PaperGo REST API](application-foundation.md) generated from the OpenAPI contract, with typed requests and responses. The [SDK README](../sdk/README.md) describes the client and how it is regenerated.

The web app's workflow editor checks scripts as you type: it completes the SDK and the API client, shows their documentation on hover, and marks type errors, against ECMAScript 2022 and the runtime's globals only.

`run(ctx, input)` receives the run's context and, for a manual start, its launch form's values:

| Field | Value |
| --- | --- |
| `ctx.run` | `id`, `workflow` (`id`, `key`, `version`), `workspace_id`, `item_id`, `items` (a selection's members in order), `event` (the event that started the run, see [triggers](#triggers)), `trigger`, `actor` (who caused the run) and `params` |
| `ctx.itemId` | the run's item, when it has one |
| `ctx.trigger` | the triggering object snapshot, also available as `ctx.run.trigger`; narrow on `kind` (`manual`, `item`, `folder`, or `file`) |
| `ctx.params` | the workflow's [parameters](#parameters) |
| `ctx.api` | the REST API, acting as the version's author |
| `ctx.wait(operation)` | waits until operations finish; see [waiting](#waiting) |
| `ctx.sleep(ms)`, `ctx.sleep({ until })` | unloads the run until a time |
| `ctx.log(...)`, `ctx.warn(...)`, `ctx.error(...)` | write to the run's log, as `console` does |

What `run` returns is the run's [output](#outputs). An exception fails the run with its message and stack; an API error is an exception with the response's status and problem.

**Triggering objects.** Item triggers expose `ctx.trigger.item`; folder triggers expose `ctx.trigger.folder`. File triggers have `kind: "file"` and expose both `item` and `file`. Item and folder snapshots contain `id`, `workspace_id`, `collection_id`, `revision_id`, `revision_number`, `content_type_id`, `name`, `keywords` and plain JSON `values`. File metadata contains `id` (the blob ID), `version`, `filename`, `content_type` (media type) and `size`. Monaco completes these fields, documents them on hover and checks access after narrowing the kind.

```ts
if (ctx.trigger?.kind === "file") {
  ctx.log(ctx.trigger.item.name, ctx.trigger.file.filename, ctx.trigger.file.version);
}
if (ctx.trigger?.kind === "item") {
  ctx.log(ctx.trigger.item.values?.title);
}
```

**Manual starts and tests** expose `kind: "manual"`, including standalone runs. `input` contains the submitted launch form results and is the same object passed as the second argument to `run`. `item` is the primary selected item's readable snapshot, when available. `items` contains every selected item in launch order as `{ id, snapshot? }`, including the primary item; a single-item run has one entry and a standalone run has an empty array. An entry keeps its ID even if the author cannot read its snapshot or the item was deleted before dispatch. Declare the workflow's input type for form-field completion in Monaco:

```ts
export default workflow<{ note: string }>({
  triggers: [{ type: "manual", selection: "selection" }],
  input_schema: { type: "object", properties: { note: { type: "string" } }, required: ["note"] },
  run(ctx) {
    if (ctx.trigger?.kind !== "manual") return;
    ctx.log(ctx.trigger.input.note, ctx.trigger.item?.name);
    for (const selected of ctx.trigger.items) ctx.log(selected.id, selected.snapshot?.name);
  },
});
```

An event with a `revision_id` captures that revision, even if the item changes before dispatch. Manual and test starts capture each selected item's readable revision at dispatch. Scheduled and other item-associated starts without a revision capture their primary item's readable revision at dispatch; schedules configured for the published surface capture the published revision. Snapshots are persisted once with the run and stay the same across waits, resumes and retries. They use the workflow version author's read permissions; unpublished item content requires draft access. Automatic events for standalone, deleted/purged or inaccessible resources have no snapshot.

Snapshots have a 64 KiB JSON budget shared by the complete selection. If field values would exceed it, `values` is absent and `values_omitted` is `true`; otherwise `values_omitted` is `false`. Form results use the run's separately stored input, and the primary item references its selection snapshot. This keeps workflows that only need identity or file metadata usable for large items. Oversized remaining metadata fails the run with a visible error. File bytes, storage keys, permissions, relationships and history are excluded. An API read returns current state, which coalescing workflows may need; snapshots contain no current ETag, so obtain one through the API before a conditional write.

**Steps and replay.** Each API request, wait, sleep, clock reading (`Date`), random value (`Math.random`, `crypto.randomUUID`) is a recorded **step**. A request runs in-process through the same handlers as an HTTP request. One that may change something (any method but `GET` and `HEAD`) runs in a transaction that also records its response, so a crash either keeps both or neither: its database writes happen once. A read runs as an API client's read does, outside the run's transactions, and its response is recorded with the next such step, every 256 reads or 4 MiB of responses, or when the run ends; a crash before then only repeats the read. A run executes in **slices**: a slice replays the script from the start, answering each recorded step from the journal instead of repeating it, and continues until the script returns, throws, waits or sleeps. A run therefore continues where it was after a restart, and nothing it wrote to the database is written twice. Work a request starts, such as OCR or a model call, runs as its own operation, which may repeat its work outside the database under its module's [recovery protocol](architecture.md#durability-boundaries).

Keep scripts deterministic: read time, randomness and data only through `ctx` and the globals the engine replaces. A replay that asks for a different step at a recorded position fails the run as nondeterministic, so a script edited while runs are active applies to new runs only: runs keep their version.

**Limits.** A slice may run JavaScript for 5 seconds before it waits; a script that computes longer fails. Each recorded response is bounded at 1 MiB, a longer one is recorded as a `413` asking for a smaller page. A run's output is bounded at 16 KiB, a script's source at 1 MiB and its log at the last 500 entries. One wait takes at most 64 operations.

## Waiting

Long work is an API request that returns an **operation** (`202 Accepted`): reading a file's text, composing a PDF, an AI completion, a task. `ctx.wait(operation)` unloads the run until the operation finishes and returns it as it ended; a failed or cancelled operation throws an `OperationError`. `ctx.wait([a, b])` waits for several at once, which run in parallel. A run that waits shows as `waiting`, holds no worker, and continues moments after the operation ends.

```ts
const read = await ctx.wait((await ctx.api.v1.items.byId(ctx.itemId!).text.post({ ocr: "auto" }))!);
const text = plain(read.result) as { text_id: string; pages: number };
```

`plain(value)` converts an operation's `result`, which the client returns as untyped JSON, to plain values. `json(value)` wraps a free-form object, such as an item's `values` or an `output_schema`, for a request body.

`ctx.sleep(ms)` and `ctx.sleep({ until })` unload the run until a time; the run shows as `waiting` with `waiting_until`.

Cancelling a run cancels the operations its requests started, such as its tasks, which it [owns](operations.md#ownership-and-dependencies). Operations it only waits for, such as another run's task, go on: waiting is a dependency, not ownership.

## Parameters

A workflow's `parameters` are a JSON object the script reads as `ctx.params`, such as the IDs of the lists it works on. Built-ins take parameters through their form, and their triggers can be a function of them:

```ts
export default workflow<unknown, unknown, { collection: string }>({
  triggers: (p) => [{ type: "file.added", collection_id: p.collection }],
  async run(ctx) { /* ... */ },
});
```

`triggers` and `condition` may be functions of the parameters; saving the workflow evaluates them. Workspace packages remap the IDs in parameters when they are imported into another workspace, so scripts keep IDs in parameters rather than in their source.

## Testing

The workflow editor saves the script and, beside it, runs its current version as a **test run** on items you choose, showing its steps and log as they happen. `POST /v1/workflows/{id}/test-runs` does the same for API clients, with `item_ids`, `primary_item_id` and `inputs` (202 with `run_id`). A test run:

- runs drafts and published workflows alike, whatever their triggers, condition and manual or automatic use;
- runs on no item, on one, or once on several, the primary (default the first) being its item, as a [selection run](#selection-runs);
- makes real changes as the version's author, like any run, so use items meant for testing;
- ignores the workflow's concurrency, so it never cancels or waits for the workflow's other runs;
- raises no `wf.*` or `role.*` events, so nothing that follows the workflow reacts to it.

Its event type is `test`, and run lists select test runs with `origin=test`. Starting one needs workspace manage access and write access to every item.

## Manual use and automatic processing

A workflow has independent `manual_enabled` and `automatic_enabled` settings. Its lifecycle is `draft`, `published` or `archived`; only published workflows accept new runs. New custom workflows in the editor and duplicated built-ins start as drafts with automatic processing off. Publishing makes the allowed entry points usable. Pausing automatic processing leaves manual use available.

**Workspace settings → Workflows** shows separate Manual use and Automatic runs summaries, and edits a workflow's script and parameters. Choose a library or list to configure its built-ins; the same view is linked from that collection's settings. Content rules (`file.*`, `item.*`, `folder.*`, `permissions.changed`) require a collection. There is no inherited workspace-wide content automation.

For example, prepare Optimize image storage in the Photos library with manual use allowed and Automatic runs off. Run it once on a selected photo without subscribing to uploads or replacements. To process future uploads in that library, turn automatic runs on and choose the active event rules. Other libraries keep their own settings. Each automatic trigger can also be disabled independently with `disabled: true`.

Select items in a list or library and choose **Run workflow**, then **Run once**, to launch an available manual workflow for that collection and selection size. The confirmation dialog
keeps the selected items, collects any launch inputs, and links to the queued run. A server preflight explains missing setup, selection or permission requirements; the start request repeats these checks.
Select individual items (up to 100); the all-matching bulk selection cannot launch
a workflow. The server rechecks write permission on every item before starting.

## Triggers

| Type | Starts a run when | The run's item |
| --- | --- | --- |
| `item.created` | an item is created (its first revision) | the item |
| `item.updated` | an item's content changes, it moves across collections, or its relationships change | the item |
| `item.published` | a revision is published, explicitly or automatically | the item |
| `item.unpublished` | an item is unpublished | the item |
| `item.deleted` | an item is deleted, alone or with its folder | none (`ctx.run.event.resource_id` names it) |
| `item.restored` | a deleted item is restored from the recycle bin | the item |
| `item.purged` | a deleted item is permanently purged | none (`ctx.run.event.resource_id` names it) |
| `file.added` | a library item receives its first file attachment | the item |
| `file.updated` | an existing file is replaced, restored, or reindexed | the item |
| `folder.created`, `folder.updated` | a folder is created, or renamed, retagged or moved | the folder |
| `folder.deleted`, `folder.purged` | a folder is deleted (alone or with a parent folder), or permanently purged | none (`ctx.run.event.resource_id` names it) |
| `folder.restored` | a deleted folder is restored from the recycle bin | the folder |
| `permissions.changed` | an item's or folder's effective permissions change: its own or an ancestor's ACL is replaced, it moves into another permission scope, or workspace membership changes. Delivered once **per affected item and folder** (see below) | the item or folder |
| `schedule` | a cron occurrence: `cron` (5 fields) and `time_zone` (IANA, default UTC) | see below |
| `manual` | someone starts it through the API or the web app | each chosen item, the selection's primary item, or none |
| `term.merged` | a term is merged into another (see [taxonomy](application-foundation.md#controlled-taxonomy)) | none |
| `term.moved` | a term moves under another parent, or a keyword is promoted into another set | none |
| `term.changed` | a term's name, localized labels or other editable properties change through PUT `/v1/terms/{id}` | none |
| `task.created`, `task.completed`, `task.cancelled` | a [task](#tasks) of the workspace is created, completed or cancelled | the task's item, or none |
| `wf.{key}.completed`, `wf.{key}.failed` | a run of workflow `key` ends | that run's item |
| `role.{role}.completed`, `role.{role}.failed` | a run of any workflow filling the [role](#roles) ends, with the same data as its `wf.{key}` event | that run's item |

Fields of a trigger:

- `collection_id` limits item and workflow-event triggers to one list or library. On `permissions.changed`, it limits deliveries to that collection's items and folders.
- `content_type_id` (with `collection_id`) limits item triggers to one content type.
- `terms` (1 to 20 term IDs) limits `item.created`, `item.updated` and `item.published` to revisions whose term and keywords fields hold one of the terms, a descendant of one, or a term merged into one. `term_change` says what counts: `present` (the default, the revision holds it), `added` (held now, not by the item's revision before) or `removed` (held before, not now).
- `task_key` (on `task.*` triggers) limits them to tasks with that key, such as `legal_review`. With `collection_id`, only tasks about items of that collection start runs.
- `sources` (on `file.added` and `file.updated`) lists the file sources that start runs: `upload`, `webdav`, `restore`, `workflow` and `reindex` (a [search reindex with content](search.md#the-store)). The default is `upload`, `webdav` and `restore`, so a workflow that replaces a file does not start itself again.
- `media_types` (on `file.added` and `file.updated`) limits the trigger to files of these media types: exact (`application/pdf`) or a wildcard (`image/*`), up to 20. Other files start no run; a script can also read `ctx.run.event.data.content_type`.
- Folder triggers take `collection_id` and `content_type_id`, and no condition: conditions test item fields.
- `actions` (on `permissions.changed`) lists the changes that start runs: `replace` (an ACL was replaced), `move` (a move gave the entry another permission scope), `member.set` and `member.remove` (workspace membership). The default is all. `permissions.changed` also takes `collection_id`, `content_type_id` and a condition, which folders never meet. `term.changed`, `term.merged` and `term.moved` take no trigger options or item conditions.
- For a schedule:
  - **With `collection_id`:** each occurrence starts one run per item of the collection that meets the condition, up to 500, on `surface` (`head` by default, or `published`).
  - **Without `collection_id`:** one run with no item.
  - Occurrences missed while PaperGo was down start once.
  - A change to a workflow never starts runs for times already past.
- Manual triggers expose **Item association** in the editor:
  - **None (Standalone)** has neither `collection_id` nor `selection`, requires workspace manage access, and rejects selected items.
  - **Single Item** uses `selection: "per_item"` and requires exactly one item.
  - **Multiple Items** uses `selection: "selection"` and starts one run for 1–100 ordered items, which the script reads as `ctx.run.items`.
  - Single and multiple modes can target any collection in the workspace or a specific `collection_id`. A collection without an explicit selection uses single-item mode.
  - A manual workflow can declare `input_schema` for its launch form (see [Forms](#forms)).

The event that started a run is `ctx.run.event`:

| Key | Value |
| --- | --- |
| `id`, `type` | the event's ID and type |
| `actor` | who made the change, started the run, or (for schedules) the version's author |
| `workspace_id`, `collection_id`, `resource_id` | where it happened |
| `depth` | how many runs led to this event |
| `data` | type-specific details |

The `data` for each event type:

- `item.created` / `item.updated`: `revision_id`, `revision_number`, `content_type_id`, `blob_id`
- `file.added` and `file.updated`: `filename`, `source`, `file_version`, `blob_id`, `content_type` (the file's media type), `size`, `revision_id`, `content_type_id`. `file.updated` also includes `previous_blob_id`. Metadata edits emit `item.updated` without a file event; content processors subscribe to the two file events.
- `permissions.changed`: `resource_kind` (`item` or `folder`), `action`, `root_id` and `root_kind` (the resource whose ACL changed, that moved, or the workspace), `root_event_id`, and `previous_scope_id` / `current_scope_id` for `replace` and `move` (the permission scope the entry read through before and after). Membership actions add `member_subject`, `previous_role` and `role`; an empty role means no membership.
- `folder.created` / `folder.updated`: `revision_id`, `revision_number`, `content_type_id` for a new name or keywords; `from_parent_id` and `parent_id` for a move
- `item.published`: `revision_id`, `revision_number`, `publication_id`
- `item.unpublished`: `revision_id`
- `schedule`: `workflow_id`, `occurrence`; item-associated schedules also include `surface` (`head` or `published`)
- `manual`: `workflow_id`; form results are available as `ctx.trigger.input` and `run(ctx, input)`'s second argument
- `term.merged`: `source_term_id`, `target_term_id`, `term_set_id`
- `term.moved`: `term_id`, `term_set_id`, `previous_term_set_id`, `parent_id`, `previous_parent_id` (empty at the root)
- `term.changed`: `term_id`, `term_set_id`, `version`, sorted `changed_fields`, and `previous` / `current` term properties (`name`, `parent_id`, `description`, `color`, `sort_order`, `labels`, `synonyms`, `deprecated`, `available_as_keyword`; absent optional fields mean unset). The event has no `resource_id` because the term is organization taxonomy, not a workspace resource.
- `task.*`: `task_id`, `kind`, `key`, `assignees`, `source`, `item_id`, `run_id`; `task.completed` adds `outcome` and `completed_by`
- `wf.*` and `role.*`: `run_id`, `workflow_id` and `status`; `failed` adds `error`, and `completed` adds the run's [`outputs`](#outputs): what its script returned

Only items raise item events; folders raise folder events.

**Permission changes reach every entry they affect.** The write itself commits one root event, so replacing the ACL of a library with 200,000 items stays a small write. The dispatcher then delivers `permissions.changed` to each affected item and folder, 500 per batch, and nothing logged after the root is dispatched before all its deliveries are, so events keep their order. A crash resumes after the last delivery. The affected entries are:

- for an ACL replace or a move: the resource and every entry below it that inherits from it, stopping at entries with their own permissions;
- for a membership change: every item and folder of the workspace, because grants to workspace roles can appear in any scope.

The entries are those the change reaches when the root is dispatched. An entry that a later change gave its own permissions, or moved, before then gets that later change's delivery instead, which arrives after both. A workflow that reads the entry's current state, such as `search.index`, therefore always sees its final permissions.

A change is expanded only when an enabled workflow of the workspace subscribes to it (its `collection_id` and `actions` match), so permission changes cost nothing when no workflow listens. An unchanged ACL or term update, or a repeated membership assignment, raises no event. Failed writes leave no events. Permission events and term events retain the actor and workflow cause/depth, and commit with the write and audit.

Updating organization terms delivers `term.changed` to every live workspace so its workflow subscriptions can react, including localized-label-only edits. Subscriptions to `term.changed` require `taxonomy.read`. Dispatch skips subscriptions whose workflow version author no longer has that scope. These events describe taxonomy changes without changing item revisions. Term creation, moves, keyword promotion and merges do not raise `term.changed`; moves and promotions raise `term.moved` and merges raise `term.merged` in every live workspace.

## Concurrency

`concurrency` decides what a start does while a run of the same workflow on the same item is still queued, running or waiting:

| Value | A new start |
| --- | --- |
| `parallel` (default) | runs beside the active run |
| `skip` | starts nothing |
| `replace` | cancels the active run and starts |
| `coalesce` | starts nothing while a run is **queued**; while one is running or waiting, queues one run that starts when it ends |

Selection runs compare every member: a start overlaps an active run when they share an item. An automatic start that `skip` or `coalesce` drops creates no run. Explicit manual starts always keep their returned run IDs: coalescing queues each manual invocation in order, and a busy `skip` policy records a failed run explaining the rejection before the script runs.

`coalesce` suits workflows that rebuild something from the item as it is when they run, such as an index: a burst of changes costs at most two runs, and the last one sees the final state. Runs of one coalescing workflow on one item never overlap: a run that starts while another is active waits, as `waiting`, until it ends. Selection workflows cannot coalesce. Cancelling a run discards its staged files, cancels the operations its requests started and the tasks it waits for.

## Conditions

`condition` uses the [query filter language](application-foundation.md) of collection queries, so a condition means exactly what the same filter means in a view:

- `and` / `or` / `not`
- typed operators
- `value_ref`, such as `today` or `me`
- system fields `$name`, `$created_at`, and so on

Every trigger of a workflow with a condition needs `collection_id`, and the condition's fields must be indexed in each of those collections. Saving checks both.

For item and workflow-event triggers, the condition is tested against the item's head when the event is dispatched (normally moments after the change commits), and a run starts only if it holds. A condition that cannot be evaluated, for example because a field is no longer indexed, starts a run that fails with the reason, so the problem is visible. Manual starts check the condition before starting. Schedules with a collection start one run per item that meets it.

## Outputs

What `run` returns is the run's output: a completed run's `result`, and `data.outputs` of its `wf.{key}.completed` event, so other workflows can follow it with typed data. A workflow can declare its form as `output_schema`:

```ts
output_schema: { type: "object", required: ["total"], properties: {
  total: { type: "integer" }, reviewer: { type: "string" }, kind: { type: "string", default: "contract" } } },
```

- The output is checked against the schema, with its defaults applied, when the run completes. An output that fails the schema fails the run with the failing path, so a broken chain shows where it broke.
- Outputs are limited to 16 KiB. Return references, such as item or file IDs, rather than content.
- Failed runs have no outputs.

A workflow that follows reads them from its event:

```ts
triggers: [{ type: "wf.measure.completed" }],
async run(ctx) {
  const total = (ctx.run.event.data.outputs as { total: number }).total;
  // ...
},
```

## Forms

Launch inputs, run outputs, built-in parameters and task forms are described by **forms**: JSON Schema objects (a draft-07 subset). A UI renders them with any JSON Schema form library, but the server never trusts a form. It applies defaults and validates every value itself.

```json
"input_schema": {
  "type": "object",
  "required": ["reviewer", "topics"],
  "properties": {
    "reviewer": {"type": "string", "title": "Reviewer", "x-papergo": {"kind": "people", "access": "write"}},
    "topics": {"type": "array", "title": "Topics", "items": {"type": "string"}, "maxItems": 3, "uniqueItems": true,
               "x-papergo": {"kind": "terms", "term_set_id": "<term set>"}},
    "related": {"type": "string", "title": "Related contract", "x-papergo": {"kind": "item", "collection_id": "<contracts list>"}},
    "priority": {"type": "string", "enum": ["low", "high"], "default": "low"},
    "options": {"type": "object", "properties": {"notify": {"type": "boolean", "default": true}}}
  }
}
```

**Keywords.**
- Structure: `type` (`string`, `number`, `integer`, `boolean`, `array`, `object`), `properties`, `required`, `items`, `additionalProperties` (boolean or a schema). Ordinary forms are closed unless they opt into additional properties.
- Constraints: `enum`, `minimum`, `maximum`, `minLength`, `maxLength`, `minItems`, `maxItems`, `uniqueItems`.
- Annotations: `default`, `title`, `description`, `format`, `examples`, `readOnly`, `writeOnly`, `deprecated` and `$comment`.
- Presentation hints: other `x-*` keys are kept for UIs and ignored by the server.
- Rejected: a keyword the server would not enforce (`pattern`, `oneOf`, ...), so a form never promises a check that does not happen. A keyword on the wrong type is rejected too.

Property names are lowercase identifiers, and forms nest at most 6 levels.

**Values.**
- Defaults fill missing properties, nested objects included.
- Missing required values, unknown names, wrong types (an `integer` must be whole), values outside `enum` or the bounds, and duplicate entries under `uniqueItems` are rejected with the failing path, such as `inputs.options.notify`.
- A manual start is refused before any run starts, and the run keeps the final values: `inputs` in the run detail, and the script's `input`.

**Domain pickers.** `x-papergo` on a string property (one value) or an array of strings (several, at most 100) makes it a picker of PaperGo objects. Its values are IDs, which the server checks against the data and the permissions of the person who submits the form. A required picker must not be empty.

| `kind` | Picks | Options |
| --- | --- | --- |
| `item` | items of the workspace the person may read | `collection_id`: only that list or library |
| `collection` | lists and libraries of the workspace the person may read | |
| `relationship` | items to use with a relationship type; no link is created, the script decides | `relationship_type_id` (required) |
| `terms` | organization taxonomy terms, active and unmerged; `select: "sets"` picks term sets | `group_id` narrows either selection; `term_set_id` and `term_ids` narrow terms only |
| `keywords` | organization keywords, not deprecated: terms of its keywords set and terms available as keywords | |
| `people` | principal subjects that have access to the workspace | `access`: `read` (default), `read_draft`, `write`, `publish` or `manage` |

Set selection uses `{"kind":"terms","select":"sets"}` and browses `GET /v1/term-sets`, returning term-set IDs. Term selection returns term IDs. Term pickers browse `GET /v1/term-sets/{id}/terms`; keyword pickers suggest from `GET /v1/keywords?q=` ; keyword field labels create terms on save, and API callers can add terms with `POST /v1/keywords` (see [taxonomy](application-foundation.md#controlled-taxonomy)).

Saving a workflow checks that collections and relationship types belong to the workspace, and that taxonomy pickers reference existing organization groups, sets and terms. Taxonomy pickers require `taxonomy.read`.

**Selection hints.** The root of a [selection](#selection-runs) workflow's launch form can carry `x-papergo-selection`, text hints for the screen that chooses and orders the items: `preview` (such as `image`), `item_label` (such as `page`), `order_label` and `primary_description`. They are presentation only.

[Task](#tasks) answers fill in forms with the same renderer, defaults and checks, with the pickers checked against the person who answers.

## Tasks

A task asks people to do something and say so: approve, confirm data, fill in a form. Scripts and people create tasks with `POST /v1/workspaces/{id}/tasks`, which needs the `item.write` scope; people also use the web app's Tasks screen. An approval is a task of kind `approval`. The model and its rules live in `internal/tasks`, shared by workflows, the API and PaperGo itself.

The Tasks screen offers Waiting for me, Approvals, Completed and All views. **Load more** appends the next page of the selected view while keeping the current task open.

```ts
const task = await ctx.api.v1.workspaces.byId(ctx.run.workspace_id).tasks.post({
  title: `Check the invoice data of ${item!.name}`,
  itemId: ctx.itemId,
  assignees: ["role:members"],
  key: "invoice_check",
  outcomes: [
    { key: "correct", label: "Correct", style: "primary" },
    { key: "fix", label: "Needs a fix", comment: "required", skipForm: true },
  ],
  inputSchema: json({ type: "object", required: ["total"], properties: { total: { type: "number", title: "Total" } } }),
  values: json({ total: 12.5 }),
  dueAt: new Date(Date.now() + 48 * 3600 * 1000),
});
const answer = plain((await ctx.wait(task!.operationId!)).result) as { outcome: string; input?: { total: number } };
```

- **Workflow tasks.** A task a run creates belongs to its workflow and run (`source: "workflow"`) and has an `operation_id`: the operation its answer ends. `ctx.wait` on it returns, as the operation's result, `task_id`, `status`, `outcome`, `completed_by`, `comment`, `input` and every answer in `responses`; a cancelled task throws an `OperationError`. Cancelling a run cancels the tasks it waits for. Tasks it created without waiting, such as a follow-up, outlive it, and other workflows react to them with `task.*` triggers, until [their item or workflow is deleted](#deleting-items-and-workflows).
- **Assignees** are recipient entries:
  - a person's subject;
  - `group:<id>`, nested groups included;
  - `role:owners` or `role:members` of the workspace;
  - `creator` (the item's creator), `actor` (who caused the run), or `field:<key>` (the people in a field of the item).

  No assignees means the workspace's owners. Unknown people or groups are refused with their names, and inactive users are left out. At most 50.
- **Access.** Assignees who are people must be able to read the workspace and, for a task about an item, the item's draft. This is checked when the task is created (naming who cannot) and again when someone answers; a group's members are checked when they answer. A task is visible to its assignees, its creator and the workspace's owners.
- **Outcomes** are the answers, in order (default one, `done`), at most 10. Each has a `key` (a lowercase name other than `error` and `expired`), a `label`, a `style` for its button (`primary`, `danger`, `default`), and:
  - `comment`: `none`, `optional` (default) or `required`;
  - `skip_form`: answers without the form, such as a rejection;
  - `allow_stale`: allows the answer when the review's staged file was made from files that are no longer current;
  - `decisive`: in `all` mode, completes the task at once.

  An approval's outcomes are `approved` and `rejected`; a rejection needs a comment.
- **Completion.** `any` (default): the first answer completes the task. `all`: every assignee answers once, a group by any one of its members; someone assigned by name and through a group answers once for each. A decisive outcome completes it at once; otherwise it completes when everyone has answered, with the outcome chosen most often (ties go to the outcome listed first). The result's `completed_by`, `comment` and `input` are the completing answer's: the latest answer with that outcome.
- **Reassigning.** When an assignee can no longer answer, for example after leaving, a workspace owner (or the creator of a person's task) replaces the open task's assignees. Answers of entries that stay count; with `all`, the task completes once every entry left has answered, and a waiting run continues.
- **Form.** `input_schema` is a [form](#forms), frozen on the task when it is created. Its pickers must point at the workspace. `values` prefill it and must be valid answers, picks included. An answer sends the form's values unless its outcome skips the form; the server applies defaults and checks them, and pickers are checked against the person who answers.
- **Review.** `review` shows a staged file of the run, an [artifact](documents.md#artifacts-and-staged-files) it staged, beside the form: `file.compare` shows the item's current file beside a staged image, both at 100%; `file.preview` shows a staged document, such as a PDF. An answer whose outcome does not `allow_stale` fails with 409 when the staged file was made from a file that is no longer current.
- **Other fields:** `key` (a lowercase name triggers and lists filter on), `priority` (`low`, `normal`, `high`) and `due_at`. A due date describes the task; it does not cancel it.

Answers and their form values stay on the task after it completes, and completed tasks are kept when their runs are deleted. Creating and answering tasks are recorded in the workspace's audit trail (`task.create`, `task.respond`).

Assignees are [notified](notifications.md) when a task is created or reassigned to them, and the creator of a person's task when it is answered. A script tells people anything else with `ctx.api.v1.workspaces.byId(id).notifications.post(...)`; a failed run notifies the author of its workflow version.

## Selection runs

A manual trigger with `"selection": "selection"` starts one run over several items. `POST /v1/workflows/{id}/runs` then takes:

- `item_ids`: 1 to 100 items of the workflow's workspace, limited to the trigger's collection when configured. Duplicates are dropped and the first occurrence keeps its place.
- `primary_item_id`: optional, one of `item_ids`. It defaults to the first.

Every item needs the starter's `write` access and must meet the condition before the run starts.

The primary item is the run's item: `ctx.itemId` and the run's events use it. `ctx.run.items` lists all members in order.

The membership is recorded with the run (`items` in the run API) and does not change:

- **Retries.** A retried run keeps the same members.
- **Run listing.** Listing runs with `item_id` includes selection runs the item is a member of.
- **Deleting a member.** Deleting a member, alone or with its folder, cancels the queued, running or waiting selection runs it belongs to, as it cancels the runs on that item. The exception is the run that deleted it, for example a run that replaces several items with one.

## Item workflow history

An item's **Workflows** tab offers applicable manual workflows and recent runs,
including runs where the item belongs to a selection. Status, trigger, actor and
execution time refresh while the tab is open. Users with draft-read access to the
item can read scoped summaries. These omit execution errors, inputs, outputs,
steps and other selection members. Workspace managers can follow **View steps**
to inspect a run's recorded steps and log and retry failed runs. General run listing, execution
details, cancellation and retry require workspace manage access.

The history view supports manual, automatic and scheduled origins, status filters and paging. Manual system runs are included by default; high-volume automatic system runs can be included explicitly. Item readers see scoped summaries; managers can inspect full results and steps.

The automatic eligibility check evaluates a chosen item against an upload, replacement or metadata-change scenario using current rules and conditions. It writes no events and runs nothing; matching a rule does not guarantee that the run succeeds.

## Who a run acts as

A run acts as the **author of its workflow version**: the subject who saved that version, or who configured the built-in. Every API request it makes is checked against that author's permissions and scopes, as an HTTP request would be. A workflow therefore never does more than its author may, and a run fails visibly if the author loses access. `ctx.run.actor` still names who caused the run.

Changes a run makes raise events like any other change, one level deeper than the event that started the run. Events at depth 5 start nothing, which stops workflows that keep triggering each other.

## Built-in workflows

Built-in workflows are processes PaperGo ships, as scripts in `internal/workflow/builtin`, in the same model.

| Key | Name | Scope | Parameters |
| --- | --- | --- | --- |
| `documents.optimize` | Optimize image storage: when an image is added (uploaded, through WebDAV or restored), or on demand, [`optimize-image`](documents.md#image-optimization) proposes a smaller copy and an approval asks the `approvers` to compare it with the current file at full size; approving promotes it as a new file version, rejecting discards it. `concurrency: replace`, so a newer file cancels a waiting review. Needs OCR. | per library | `target_height`, `min_confidence`, `percentile`, `max_analysis_dimension`, `minimum_scale`, `quality`, `approvers` (people; none means the workspace's owners) |
| `documents.photos_to_pdf` | Photos to PDF: started on a selection (`selection: "selection"`, launch screen with image previews and a page order), [`compose-pdf`](documents.md#photos-to-pdf) combines the photos into one searchable PDF and an approval asks the `approvers` to review it (`file.preview`). Approving promotes it as the primary item's new file version, keeping its metadata and history, and sends the other photos to the recycle bin in the same transaction; rejecting discards it. `concurrency: skip`. Needs OCR. | per library | `target_height` (default 32), `quality` (default 80), `approvers` |
| `search.content` | Index file content: when a PDF or an image is added (also through WebDAV, a restore, a workflow or a content reindex), reads its text, chunks, embeds and publishes it ([search](search.md#file-content)). `concurrency: coalesce`. A system built-in filling the `search.content` role, on by default when the deployment runs search; outputs `blob_id`, `text_id`, `pages`, `language`, `generation`, `chunks` | per list or library (manual catalog entry available) | `ocr`, `max_pages`, `chunker`, `max_chars`, `overlap`, `max_chunks`, `embed` |
| `search.index` | Index for search: on item and folder changes, `permissions.changed` (`replace`, `move`), `term.changed`, `term.merged`, `term.moved` and manual starts, `POST /v1/workspaces/{id}/search/sync` queues the entry's [search](search.md) documents to be rewritten, in batches; term events resync every entry holding the term or a term below it. `concurrency: coalesce`. A system built-in filling the `search.index` role, on by default when the deployment runs search. | per list or library (manual catalog entry available) | none |
| `documents.preview` | Generate file previews: renders a [preview](previews.md) when a file is added or replaced, and on demand. A system built-in filling the `file.preview` role, on by default. | per list or library | none |
| `items.expire` | Unpublish expired items: every day, unpublishes the published items whose date `field` is before today | per list or library | `field` (an indexed date field), `cron` (default `0 1 * * *`), `time_zone` (default `UTC`) |

How people use them:

- **Configure use and parameters:** `PUT /v1/workspaces/{id}/workflow-builtins/{key}` with `collection_id`, `automatic_enabled`, `manual_enabled`, `state`, `disabled_triggers` (automatic event names) and `parameters`. The catalog supplies typed `parameters_schema`, manual selection mode, supported file types and automatic rules. The first call creates the configuration; later calls require its ETag in `If-Match`.
- **Run once:** manual admission is independent of automatic subscriptions and role provider selection. The server checks the selection, inputs, caller permissions and known execution dependencies, such as OCR. The accepted event pins the workflow version, including its execution author. Editing, pausing or archiving afterward does not discard accepted work.
- **Duplicate to customize:** `POST .../workflow-builtins/{key}/copy` creates an ordinary draft workflow with the built-in's script and parameters and automatic processing off. The source configuration stays as configured. Publish the copy and explicitly enable automatic processing to replace a provider in that collection.
- **Release updates:** when the API starts, built-ins whose script changed in the running release receive it as a new immutable version. Their manual/automatic policies and disabled event rules are retained. Built-ins whose parameters no longer fit pause their automatic processing.

### Roles

Some processes are **roles**: product processes that one workflow fills, which people can turn off or replace with their own process. The catalog lists them in `roles`, each with the `output_schema` its fillers return.

- **Providers.** A workflow declares a role and compatible outputs. Every provider raises `role.{role}.completed` or `role.{role}.failed`, letting other workflows follow the feature regardless of which implementation supplies it.
- **Collection scope.** Automatic role providers require `collection_id`. Enabling a published provider pauses other automatic providers of that role in the same collection. Their manual use stays independent, and other collections are unaffected. A draft copy does not claim automatic ownership.
- **Defaults.** Default role built-ins are prepared per collection under the workspace creator. Listing the catalog or dispatching the first event prepares missing defaults. Explicit paused, replaced or deleted configurations are preserved.
- **Inspect providers:** `GET /v1/resources/{id}/workflow-roles` lists each role, its configured workflow, `scope: "collection"` and `automatic_enabled`.
- **System built-ins.** Their successful automatic runs are kept for `SYSTEM_RUN_RETENTION` (default 24h), or for `RUN_RETENTION` when that is shorter. Manual runs remain visible in ordinary history and use normal run retention. They may receive taxonomy events without exposing that data to users.

## Runs

| Status | Meaning |
| --- | --- |
| `queued` | waiting for a worker |
| `running` | a slice of its script is executing |
| `waiting` | waiting for operations, a time (`waiting_until`), or the active run of a coalescing workflow, without holding a worker |
| `completed` | the script returned |
| `failed` | the script threw, broke a limit, replayed differently, or could not start |
| `cancelled` | stopped by a manager, replaced, or its item or workflow was deleted |

`GET /v1/workflow-runs/{id}` shows the run's result, its launch `inputs`, its log and its recorded steps: each API request with its response, each wait with the operations it waited for, and each value the script read.

- **Retry** (`POST .../retry`) runs a failed run again as a new run of the same version, inputs and parameters, with `retry_of`. Its steps before the last request, wait or sleep of the failed run replay their recorded results, so nothing they wrote is written twice; that last step and what followed run again.
- **Cancel** (`POST .../cancel`) stops a queued, running or waiting run, with the operations its requests started and the tasks it waits for.

**How runs start.** A change and its events commit together in the domain event log (`domain_events`), so a change that rolls back raises nothing, and the write does no workflow work. The dispatcher takes logged events in order, matches them to workflows, and starts the runs. The runs and the events' dispatched mark commit together, and each event starts at most one run per workflow, so a crash dispatches events again without duplicates. The dispatcher wakes when a change commits and also checks every few seconds, so it picks up events a restart left behind.

Each run is an operation of kind `script.run`, which the `workflows` worker executes a slice at a time. A manual start returns run IDs at once. Until its event is dispatched, the run shows as `queued`.

Run lists leave out the runs of system built-ins, which run on every change, unless `workflow_id` selects their workflow or `item_id` selects the item being inspected.

Finished runs, with their steps and logs, and dispatched events are deleted after `RUN_RETENTION` (default 30 days). Successful automatic runs of system built-ins are deleted after `SYSTEM_RUN_RETENTION` (default 24h), or `RUN_RETENTION` when that is shorter. Failures and manual system runs use `RUN_RETENTION`. Queued, running and waiting runs stay.

### Deleting items and workflows

Deleting an item, alone or with its folder, moves it to the recycle bin and moments later cancels the work pending on it:

- the queued, running and waiting runs on it, and the selection runs it belongs to, with the operations they own;
- the open tasks about it, whoever created them;
- the pending files staged to replace its file, a failed run's included;
- the pending operations on it, such as a text read an API client asked for.

The run whose own request deleted the item keeps its work, for example a run that replaces several photos with one PDF. Restoring the item from the recycle bin restores none of it and starts no workflow; run one by hand to process the item again.

Deleting a workflow, alone or with its workspace or collection, raises `workflow.deleted`, which cancels its active runs, the open tasks of all its runs, and their pending staged files. Its versions and finished runs are kept.

| Record | Lifetime | Collected |
| --- | --- | --- |
| Finished runs, failed runs, manual system runs, dispatched events | `RUN_RETENTION` (default 720h) | Hourly |
| Successful automatic system runs | `SYSTEM_RUN_RETENTION` (default 24h), or `RUN_RETENTION` when shorter | Hourly |
| Reusable answers | `RESULT_CACHE_TTL` (default 720h), extended on read | Hourly |
| Live changes | 7 days | Hourly |
| Delta tokens | 30 days; a materialized page lasts 5 minutes | Hourly |
| Other terminal operations | 30 days; pending operations stay | Hourly |
| Content packages | 7 days on the export result | Blob collection after that result expires |
| Item and file history | The collection's versioning and `max_versions` | On the collection's own writes |
| Audit events | Kept. The schema rejects updates and deletes | |
| Publication snapshots | Kept while the item is live. History settings do not remove them | |

Audit events and publication snapshots are a separate product record. Run retention, history limits and cache expiry do not delete them, and their schema guards stay in place. Turning user-visible history off keeps the live and published snapshots, including a calendar item's publication.

## Permissions

| Action | Needs |
| --- | --- |
| List and read workflows | workspace `read` |
| Create, change, delete workflows; turn on, configure and copy built-ins | workspace `manage` (built-ins per collection also need collection `manage`) |
| Start a manual workflow on items | `write` on each item |
| Start a manual workflow with no item | workspace `manage` |
| Start a test run | workspace `manage`, and `write` on each item |
| See, cancel and retry runs | workspace `manage` |
| Answer a task | being an assignee (or in an assigned group) who can read the workspace and the item's draft |
| Create a task | workspace `read`, and `write` on the item when it concerns one |
| Cancel a task | its creator or a workspace owner |
| See a task | being an assignee, its creator or a workspace owner |

## API

| Method | Route | Purpose |
| --- | --- | --- |
| GET | /v1/workflow-catalog | Trigger types, roles and built-ins |
| GET / POST | /v1/workspaces/{id}/workflows | List or create workflows: `{"name", "description", "source", "parameters", "automatic_enabled", "manual_enabled", "state"}` |
| GET / PUT / DELETE | /v1/workflows/{id} | Read, replace (If-Match), delete (If-Match) |
| GET | /v1/workflows/{id}/versions | Versions with their scripts, newest first |
| POST | /v1/workflows/{id}/runs | Manual start: `{"item_ids": [...], "inputs": {...}, "workflow_version": 1}` (202 with `run_ids`) |
| POST | /v1/workflows/{id}/test-runs | [Test run](#testing) of the current version, draft or published: `{"item_ids": [...], "primary_item_id", "inputs": {...}}` (202 with `run_id`) |
| POST | /v1/workflows/{id}/check | Read-only manual preflight with the same launch body |
| POST | /v1/workspaces/{id}/workflow-actions | Manual actions and availability reasons for a selection |
| POST | /v1/workflows/{id}/explain | Read-only automatic eligibility for an item and event scenario |
| GET | /v1/workspaces/{id}/workflow-builtins | Built-ins with the workspace's built-in workflows |
| PUT | /v1/workspaces/{id}/workflow-builtins/{key} | Turn on/off, set parameters |
| POST | /v1/workspaces/{id}/workflow-builtins/{key}/copy | Copy into an editable workflow |
| GET | /v1/workspaces/{id}/workflow-runs | Runs, newest first (`workflow_id`, `item_id`, `origin` (`manual`, `schedule`, `test` or `automatic`), `status`, `include_system`, `$skiptoken`, `$top`) |
| GET | /v1/workflow-runs/{id} | A run with its result, steps and log |
| POST | /v1/workflow-runs/{id}/cancel, /retry | Cancel, or retry a failed run |
| GET | /v1/me/tasks | The caller's tasks (`waiting=true` for those still to answer, `status`, `kind`, `key`, `workspace_id`, `item_id`, `$top`); open ones by due time, in pages linked by `@odata.nextLink` like the other task lists |
| GET / POST | /v1/workspaces/{id}/tasks | A workspace's tasks, for its owners; create a task: `{"title", "description", "assignees", "item_id", "kind", "key", "completion", "outcomes", "input_schema", "values", "review", "priority", "due_at"}` |
| GET | /v1/items/{id}/tasks | The tasks about an item the caller may see |
| GET | /v1/tasks/{id} | A task with its answers, `can_respond` and `answered` |
| POST | /v1/tasks/{id}/responses | Answer: `{"outcome", "comment", "input"}` |
| POST | /v1/tasks/{id}/reassign | Replace an open task's `assignees` (owners, and the creator of a person's task) |
| POST | /v1/tasks/{id}/cancel | Cancel an open task |

## Runtime

Runs execute on the API's [worker runtime](application-foundation.md#background-operations) with every other operation: the `workflows` worker runs `script.run` operations, four slices at a time by default. Maintenance tasks dispatch events (and wake on every commit that logged events), raise due schedules each minute, release the waits of operations that ended while the process was stopped, and apply run retention hourly.

| Setting | Meaning |
| --- | --- |
| `RUN_RETENTION` | How long finished runs, failed runs and dispatched events are kept (default `720h`). This is also the ceiling for successful automatic system runs |
| `SYSTEM_RUN_RETENTION` | How long successful automatic system runs are kept (default `24h`) |
| `RESULT_CACHE_TTL` | Idle lifetime of reusable answers (default `720h`). Independent of `RUN_RETENTION` |

## Building features on workflows

PaperGo code that should happen after a change or on a schedule:

1. Adds an API route, or an operation for long work, in a feature module (see [workflow extensions](workflow-extensions.md)). Routes change data through `dms` as the caller, so permissions, validation and events stay in one place, and scripts call them like any client.
2. Ships the process as a built-in workflow when people should see or change it.
3. Never reacts to changes in hidden code paths.

Relationship creation, attribute updates, deletion and taxonomy merges emit `item.updated` for each live endpoint. These writes advance both item ETags and modification timestamps without adding metadata history. Their data includes `relationship_id`, `relationship_action`, `peer_id`, `changed_fields: ["$relationships"]`, the head `revision_id` and `content_type_id`. Term filters can match present terms; added/removed term filters do not fire for relationship-only changes. Duplicate link requests leave both endpoints unchanged. Purging an item emits relationship changes for surviving peers.

## Archive events

`item.archived` and `item.unarchived` identify the affected item and collection and carry `data.archived` (`true` or `false`). They commit with the transition and its audit entry, without a content revision or file event. The search-index built-in subscribes to both. Workflows may call `POST /v1/items/{id}/archive` and `/unarchive` with the current `If-Match`, or use collection bulk archive actions. No automatic archive policy runs unless configured in a workflow.
