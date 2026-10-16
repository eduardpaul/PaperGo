# Workflows

Workflows are how PaperGo does anything that happens **after** a change or **on a schedule**. A workflow is data that workspace managers can read, change, turn off or replace. That includes the processes PaperGo ships, which are *built-in workflows* rather than hidden code. New features that react to changes are delivered as workflow activities and built-in workflows.

A workflow has:

- **triggers**: what starts a run, such as an item being created, a cron schedule, a manual start, or another workflow's event;
- an optional **condition**: a query filter the run's item must meet;
- a **flow**: nodes that each run one **activity**, connected by outcome **ports**;
- optional **outputs**: what a completed run returns, for the workflows that follow it.

Every save that changes the definition creates a new immutable **version**. A run always executes the version it started with.

Runs are durable. Each node runs as one step that commits exactly once. A run continues at the node it reached after a crash or restart, and waits such as `delay` and parked activities survive restarts. The engine is described in the [runner plan](plans/runner.md).

## Example

```json
{
  "name": "Review new contracts",
  "definition": {
    "triggers": [{"type": "item.created", "collection_id": "<contracts list>"}],
    "condition": {"field": "status", "op": "eq", "value": "new"},
    "variables": {"limit": 10000},
    "flow": {
      "start": "big",
      "nodes": {
        "big": {"activity": "if", "inputs": {"left": "{item:values.amount}", "op": "gt", "right": "{var:limit}"}, "next": {"true": "flag", "false": "file"}},
        "flag": {"activity": "item.update", "inputs": {"values": {"status": "review", "note": "Large contract from {trigger:actor}"}}, "next": {"done": "tell"}},
        "tell": {"activity": "event.raise", "inputs": {"event": "flagged"}},
        "file": {"activity": "item.publish", "retry": {"attempts": 3, "delay": "5m"}}
      }
    }
  }
}
```

Select items in a list or library and choose **Run workflow** to launch an enabled
manual workflow for that collection and selection size. The confirmation dialog
keeps the selected items, collects any launch inputs, and links to the started run.
Select individual items (up to 100); the all-matching bulk selection cannot launch
a workflow. The server rechecks write permission on every item before starting.

## Triggers

| Type | Starts a run when | The run's item |
| --- | --- | --- |
| `item.created` | an item is created (its first revision) | the item |
| `item.updated` | an item's content changes, it moves across collections, or its relationships change | the item |
| `item.published` | a revision is published, explicitly or automatically | the item |
| `item.unpublished` | an item is unpublished | the item |
| `item.deleted` | an item is deleted, alone or with its folder | none (`{trigger:resource_id}` names it) |
| `item.restored` | a deleted item is restored from the recycle bin | the item |
| `item.purged` | a deleted item is permanently purged | none (`{trigger:resource_id}` names it) |
| `file.added` | a library item receives its first file attachment | the item |
| `file.updated` | an existing file is replaced, restored, or reindexed | the item |
| `folder.created`, `folder.updated` | a folder is created, or renamed, retagged or moved | the folder |
| `folder.deleted`, `folder.purged` | a folder is deleted (alone or with a parent folder), or permanently purged | none (`{trigger:resource_id}` names it) |
| `folder.restored` | a deleted folder is restored from the recycle bin | the folder |
| `permissions.changed` | an item's or folder's effective permissions change: its own or an ancestor's ACL is replaced, it moves into another permission scope, or workspace membership changes. Delivered once **per affected item and folder** (see below) | the item or folder |
| `schedule` | a cron occurrence: `cron` (5 fields) and `time_zone` (IANA, default UTC) | see below |
| `manual` | someone starts it through the API | each chosen item, the selection's primary item, or none |
| `term.merged` | a term is merged into another (see [taxonomy](application-foundation.md#controlled-taxonomy)) | none |
| `term.changed` | a term's name, localized labels or other editable properties change through PUT `/v1/terms/{id}` | none |
| `task.created`, `task.completed`, `task.cancelled` | a [task](#tasks) of the workspace is created, completed or cancelled | the task's item, or none |
| `wf.{key}.{event}` | a run of workflow `key` raises `event`; `completed` and `failed` are raised when a run ends | that run's item |
| `role.{role}.completed`, `role.{role}.failed` | a run of any workflow filling the [role](#roles) ends, with its `outputs` in `data`; the same data as its `wf.{key}` event | that run's item |

Fields of a trigger:

- `collection_id` limits item and workflow-event triggers to one list or library. On `permissions.changed`, it limits deliveries to that collection's items and folders.
- `content_type_id` (with `collection_id`) limits item triggers to one content type.
- `terms` (1 to 20 term IDs) limits `item.created`, `item.updated` and `item.published` to revisions whose term and keywords fields hold one of the terms, a descendant of one, or a term merged into one. `term_change` says what counts: `present` (the default, the revision holds it), `added` (held now, not by the item's revision before) or `removed` (held before, not now).
- `task_key` (on `task.*` triggers) limits them to tasks with that key, such as `legal_review`. With `collection_id`, only tasks about items of that collection start runs.
- `sources` (on `file.added` and `file.updated`) lists the file sources that start runs: `upload`, `webdav`, `restore`, `workflow` and `reindex` (a [search reindex with content](search.md#the-store)). The default is `upload`, `webdav` and `restore`, so a workflow that replaces a file does not start itself again.
- `media_types` (on `file.added` and `file.updated`) limits the trigger to files of these media types: exact (`application/pdf`) or a wildcard (`image/*`), up to 20. Other files start no run; a flow can also branch on `{trigger:data.content_type}`.
- Folder triggers take `collection_id` and `content_type_id`, and no condition: conditions test item fields.
- `actions` (on `permissions.changed`) lists the changes that start runs: `replace` (an ACL was replaced), `move` (a move gave the entry another permission scope), `member.set` and `member.remove` (workspace membership). The default is all. `permissions.changed` also takes `collection_id`, `content_type_id` and a condition, which folders never meet. `term.changed` and `term.merged` take no trigger options or item conditions.
- For a schedule:
  - **With `collection_id`:** each occurrence starts one run per item of the collection that meets the condition, up to 500, on `surface` (`head` by default, or `published`).
  - **Without `collection_id`:** one run with no item.
  - Occurrences missed while PaperGo was down start once.
  - A change to a workflow never starts runs for times already past.
- Manual triggers expose **Item association** in the editor:
  - **None (Standalone)** has neither `collection_id` nor `selection`, requires workspace manage access, and rejects selected items.
  - **Single Item** uses `selection: "per_item"` and requires exactly one item.
  - **Multiple Items** uses `selection: "selection"` and starts one run for 1–100 ordered items. Use `{run:items}` and `forEach` to process them.
  - Single and multiple modes can target any collection in the workspace or a specific `collection_id`. A collection without an explicit selection uses single-item mode.
  - A manual workflow can declare `input_schema` for its launch form (see [Forms](#forms)).

Trigger data, readable as `{trigger:...}`:

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
- `folder.created` / `folder.updated`: `revision_id`, `revision_number`, `content_type_id` for a new name or tags; `from_parent_id` and `parent_id` for a move
- `item.published`: `revision_id`, `revision_number`, `publication_id`
- `item.unpublished`: `revision_id`
- `schedule`: `workflow_id`, `occurrence`
- `manual`: `workflow_id`, `inputs`
- `term.merged`: `source_term_id`, `target_term_id`, `term_set_id`
- `term.changed`: `term_id`, `term_set_id`, `version`, sorted `changed_fields`, and `previous` / `current` term properties (`name`, `parent_id`, `description`, `color`, `sort_order`, `labels`, `synonyms`, `deprecated`, `available_as_keyword`; absent optional fields mean unset). The event has no `resource_id` because the term is organization taxonomy, not a workspace resource.
- `task.*`: `task_id`, `kind`, `key`, `assignees`, `source`, `item_id`, `run_id`; `task.completed` adds `outcome` and `completed_by`
- `wf.*`: `run_id` and the raised data; `completed`/`failed` add `status`, `node` and, on failure, `error`; `completed` adds the run's [`outputs`](#outputs) when the workflow declares them

Only items raise item events; folders raise folder events.

**Permission changes reach every entry they affect.** The write itself commits one root event, so replacing the ACL of a library with 200,000 items stays a small write. The dispatcher then delivers `permissions.changed` to each affected item and folder, 500 per batch, and nothing logged after the root is dispatched before all its deliveries are, so events keep their order. A crash resumes after the last delivery. The affected entries are:

- for an ACL replace or a move: the resource and every entry below it that inherits from it, stopping at entries with their own permissions;
- for a membership change: every item and folder of the workspace, because grants to workspace roles can appear in any scope.

The entries are those the change reaches when the root is dispatched. An entry that a later change gave its own permissions, or moved, before then gets that later change's delivery instead, which arrives after both. A workflow that reads the entry's current state, such as `search.index`, therefore always sees its final permissions.

A change is expanded only when an enabled workflow of the workspace subscribes to it (its `collection_id` and `actions` match), so permission changes cost nothing when no workflow listens. An unchanged ACL or term update, or a repeated membership assignment, raises no event. Failed writes leave no events. Permission events and term events retain the actor and workflow cause/depth, and commit with the write and audit.

Updating organization terms delivers `term.changed` to every live workspace so its workflow subscriptions can react, including localized-label-only edits. Subscriptions to `term.changed` require `taxonomy.read`. Dispatch skips subscriptions whose workflow version author no longer has that scope; each executing node rechecks it before reading the trigger payload, including after a durable wait. Revocation stops subsequent nodes with an access error. These events describe taxonomy changes without changing item revisions. Term creation, moves, keyword promotion and merges do not raise `term.changed`; merges raise `term.merged` in every live workspace.

## Concurrency

`concurrency` decides what a start does while a run of the same workflow on the same item is still queued, running or waiting:

| Value | A new start |
| --- | --- |
| `parallel` (default) | runs beside the active run |
| `skip` | starts nothing |
| `replace` | cancels the active run and starts |
| `coalesce` | starts nothing while a run is **queued**; while one is running, queues one run that starts when it ends |

Selection runs compare every member: a start overlaps an active run when they share an item. An automatic start that `skip` or `coalesce` drops creates no run. Explicit manual starts always keep their returned run IDs: coalescing queues each manual invocation in order, and a busy `skip` policy records a failed run explaining the rejection before any activity executes.

`coalesce` suits workflows that rebuild something from the item as it is when they run, such as an index: a burst of changes costs at most two runs, and the last one sees the final state. Runs of one workflow on one item never overlap: a durable wait keeps its DBOS queue partition until that run finishes or is cancelled. Selection workflows cannot coalesce. Cancelling a run discards its staged files, cancels the tasks it waits for, closes its waits and releases claims it holds.

## Conditions

`condition` uses the [query filter language](application-foundation.md) of collection queries, so a condition means exactly what the same filter means in a view:

- `and` / `or` / `not`
- typed operators
- `value_ref`, such as `today` or `me`
- system fields `$name`, `$created_at`, and so on

Every trigger of a workflow with a condition needs `collection_id`, and the condition's fields must be indexed in each of those collections. Saving checks both.

For item and workflow-event triggers, the condition is tested against the item's head when the event is dispatched (normally moments after the change commits), and a run starts only if it holds. A condition that cannot be evaluated, for example because a field is no longer indexed, starts a run that fails with the reason, so the problem is visible. Manual starts check the condition before starting.

## Flow

`flow.start` names the first node. Each node has:

- `activity`
- `inputs`, whose strings may contain tokens
- `next`, mapping ports to node names
- an optional `retry`: `attempts` 1 to 10 in all, and a `delay` of up to `24h`

After a node runs:

- The activity's outcome picks a port. Actions use `done`; `if` uses `true` or `false`.
- An outcome port that is not connected falls back to `done`. When `done` is not connected either, the run completes.
- When a node fails, it is retried by its `retry` policy. After that, the run follows its `error` port (the node output is `{"error": "..."}`), or fails at that node.
- `end` completes the run; `fail` fails it with a message.

Node names use letters, digits, spaces, `_` and `-`. Every node must be reachable from `start`. Cycles are allowed: a run visits at most 1,000 nodes outside [loop](#loops) passes and 1,000 in each pass, 100,000 in all, and a node's output is limited to 64 KB.

### Loops

A `forEach` node runs its body once per element:

- A pass ends at a node with nothing next (no connected port, and no `done`). The next element then starts at the body port again.
- After the last element, or with no elements, the loop follows `done`. Without `done`, the run continues with an enclosing loop, or completes.
- Reaching a `forEach` node through a port starts it from its first element, so an inner loop starts again on every pass of an outer loop.
- `end` and `fail` inside a body end the run.

```json
"members": {"activity": "forEach", "inputs": {"items": "{run:items}", "as": "member"}, "next": {"body": "tag", "done": "finish"}},
"tag": {"activity": "item.update", "inputs": {"item_id": "{var:member}", "tags": ["reviewed"]}}
```

## Tokens

Strings in node inputs can read run data:

| Token | Value |
| --- | --- |
| `{item:path}` | the run's item: `id`, `name`, `tags`, `values.<field>`, `collection_id`, `content_type_id`, `version`, `published`, `created_by`, `updated_by`, `created_at`, `updated_at` |
| `{trigger:path}` | the event that started the run (see above) |
| `{input:path}` | the launch form's values, defaults applied, such as `{input:reviewer}` or `{input:options.mode}` |
| `{var:name}` | run variables (`variables`, and `set_variable`) |
| `{step:node.path}` | a node's output, such as `{step:read.values.total}` |
| `{run:path}` | `id`, `workflow_id`, `workflow_key`, `version`, `actor`, `items` (the run's items in order: a selection's members, else its item) |
| `{now}`, `{today}` | the time the node ran (RFC 3339, or the date) |

A string that is exactly one token keeps the value's JSON type: `"total": "{input:total}"` writes a number. Tokens inside longer text become text, and lists are joined with `, `. A missing path is empty. Write `{{` and `}}` for literal braces.

## Outputs

A workflow can declare what a completed run returns, so other workflows can follow it with typed data:

```json
"output_schema": {"type": "object", "required": ["total"], "properties": {
  "total": {"type": "integer"}, "reviewer": {"type": "string"}, "kind": {"type": "string", "default": "contract"}}},
"outputs": {"total": "{step:read.values.total}", "reviewer": "{trigger:actor}"}
```

- `output_schema` is a [form](#forms). `outputs` maps its properties to values, usually [tokens](#tokens). Every output must be a property of the schema; `outputs` without `output_schema` is refused.
- Outputs are evaluated when the run **completes**, in its `finish` step, and checked against the schema with its defaults applied. A token that finds nothing leaves its output unset. Outputs that fail the schema fail the run at `finish`, with the failing path, so a broken chain shows where it broke.
- Outputs are limited to 16 KB. Pass references, such as item or file IDs, rather than content.
- A completed run's outputs are in its `result.outputs` (`GET /v1/workflow-runs/{id}`) and in its `wf.{key}.completed` event as `data.outputs`. A workflow that follows reads them as `{trigger:data.outputs.total}`.
- Failed runs have no outputs.

```json
"triggers": [{"type": "wf.measure.completed"}],
"flow": {"start": "set", "nodes": {"set": {"activity": "item.update", "inputs": {"values": {"total": "{trigger:data.outputs.total}"}}}}}
```

## Activities

`GET /v1/workflow-catalog` lists every activity with its ports, an `input_schema` describing its node inputs and an `output_schema` describing what `{step:node...}` can read. Both are [forms](#forms), so an editor can render a node's settings without knowing the activity. Node inputs may be tokens, so saving checks only that required inputs are present and every input is known; types are checked when the node runs.

| Activity | Kind | Does | Output |
| --- | --- | --- | --- |
| `if` | flow | `filter` (a query filter on the item's head), or `left` `op` `right` (`eq ne gt ge lt le contains empty not_empty`; numbers compare as numbers); ports `true`/`false` | `result` |
| `set_variable` | flow | sets `name` to `value` | `name`, `value` |
| `forEach` | flow | runs the nodes from its `body` port once per element of `items` (an array or a single token such as `{run:items}`, at most 1,000), with the element in the variable `as` and its position in `as_index`; see [loops](#loops) | `items`, `count`, `as` |
| `delay` | flow | waits `duration` (`90m`, `48h`) or `until` a date or time, durably | `until` |
| `task` | flow | asks `assignees` to do something and answer with one of its `outcomes`, optionally filling in a form; with `wait` (default), follows the port named after the outcome. See [tasks](#tasks) | `task_id`, `status`, `outcome`, `completed_by`, `comment`, `input`, `responses` |
| `approval` | flow | a `task` of kind `approval` with the outcomes `approved` and `rejected` (a rejection needs a comment); takes the other `task` inputs | as `task` |
| `event.raise` | flow | raises `wf.{key}.{event}` with `data` and the run's item | `event` |
| `item.has_terms` | flow | tests whether the item's term and keywords fields hold `terms` (or descendants, or terms merged into them): any of them, or all with `match: "all"`; ports `matched`/`unmatched` | `matched`, `terms` (those held) |
| `end` / `fail` | flow | completes the run / fails it with `message` | |
| `item.get` | action | reads an item | the item |
| `item.update` | action | sets `content_type_id`, `name`, `tags`, and `values` (merged; `null` removes a value) | the item |
| `item.create` | action | creates an item in `collection_id` (optional `parent_id`, `content_type_id`, `tags`, `values`) | `item_id`, `item` |
| `items.query` | action | queries `collection_id` with `filter`, `sort`, `limit` (≤ 100), on the head surface | `items`, `count` |
| `item.publish` | action | publishes the head revision, if not already published | `published` |
| `item.unpublish` | action | unpublishes, if published | `unpublished` |
| `item.delete` | action | deletes the item | `deleted` |
| `relationship.list` | action | lists one page of the item's relationships (`direction` `both`, `outgoing` or `incoming`; optional `type` name; `limit` at most 100). Pass `next_cursor` back as `cursor` | `relationships` (`id`, `source_id`, `target_id`, `type_id`, `directed`, `related_id`), `next_cursor`, `count` |
| `relationship.create` | action | links the item to `target_id` with relationship type `type` (its name). An existing link of the same type and direction is kept, so a retry does not add a second one. `directed` follows the type when omitted | `relationship_id`, `source_id`, `target_id` |
| `relationship.remove` | action | removes `relationship_id` of the item. A relationship that is already gone is reported as removed | `relationship_id`, `removed` |
| `task.cancel` | action | cancels an open task of the workspace that no run waits for (`task_id`), such as a follow-up created without waiting; a task a run waits for ends with its run | `task_id`, `status` |
| `file.promote` | action | makes a staged file of this run (`staged_file_id`) its item's current file, as a new file version; fails when a file it was made from is no longer current. With `recycle_sources: true`, recycles the other source items in the same transaction | `item_id`, `blob_id`, `file_version` |
| `file.discard` | action | discards a staged file of this run; the item keeps its file | `discarded` |
| `file.reclaim` | action | asks to reclaim `blob_id` after the grace period. The version keeps its id, number, filename, media type, size and checksum. A current file, a published file, a staged file or a byte lease held by work or a wait defers reclamation. A retry does not move the deadline. See [reclaiming a file version](#reclaiming-a-file-version) | `blob_id`, `availability`, `deferred`, `reason`, `reclaim_after` |
| `file.compose_pdf` | action | combines the current files of the run's items (JPEG, PNG or WebP photos of one list or library) into one searchable PDF in the run's order, staged for the primary item; see [photos to PDF](#photos-to-pdf). Heavy work. | `staged_file_id`, `item_id`, `others` (the other items, in order), `pages`, `bytes`, `metrics` |
| `file.optimize_image` | action | analyzes the text of the item's current JPEG, PNG or WebP image with [OCR](ocr.md) and stages a smaller WebP copy whose smallest text keeps `target_height`; ports `candidate`/`skipped`; see [image optimization](#image-optimization). Heavy work. | `staged_file_id` (candidate) or `reason` (skipped), `item_id`, `blob_id`, `file_version`, `metrics` |
| `ai.complete` | action | asks the [chat model](#ai-steps) to do what `prompt` says with `input` (text or field values), `chunk` (from `search.read_chunks`), `text_id` (from `document.read_text` for the item's file, `blob_id` optional) or `image` (a file version, alone or with text), cut at `max_chars` (default 20,000): summarize, extract, clean up. With `output_schema` (a form schema of an object), the answer is a JSON object checked against it, else text. `execution` is `immediate` or `batch` (empty uses the deployment default) and `queue` names a batch queue. Equal requests reuse the cached answer. Heavy work | `text` or `json`, `cached`, `model` |
| `ai.decide` | action | answers ordered choice, noul or ordinal score questions from `input`, JSON `data`, or guarded document `text_id` and `blob_id`; see [workflow decisions](decisions.md). Heavy work | `answers`, `model`, `input_hash`, `cached`, document `source` |
| `taxonomy.decide` | action | evaluates managed candidates of `term_set_id`, independently (`multiple`) or jointly (`single`); ports `done` and `none`; see [workflow decisions](decisions.md). Heavy work | `term_ids`, `scores`, `taxonomy_guard`, `source`, `model`, `input_hash`, `cached` |
| `tags.apply` | action | adds `tags` while keeping existing tags; requires the source `blob_id` to remain current | item snapshot |
| `document.read_text` | action | reads the text of the item's current file page by page (see [reading text](#reading-text)): `ocr` (`auto`, `always`, `never`), `max_pages` (default 500, at most 2,000), `languages` (OCR codes such as `eng+deu`; default the file's). Ports `done`, `no_text` (the file holds none) and `unsupported` (another format, a protected or broken PDF). Heavy work | `text_id`, `item_id`, `blob_id`, `pages`, `chars`, `language`, `ocr_pages`, `skipped_pages`, `truncated`, `reused`, `reason` (unsupported) |
| `search.chunk` | action | splits the text of the item's file (`text_id` of `document.read_text`, or `text` given directly, such as a summary) with a `chunker` (`window`, `pages`, `paragraphs`, `headings`, `whole`; `max_chars`, `overlap`, `max_chunks`) into chunks of a `kind` (`content`, `summary`, `extract`), and stages them in the search index under a new `generation`, or one of this run given to add to. See [file content steps](#file-content-steps). Heavy work | `generation`, `chunks`, `truncated`, `item_id`, `blob_id` |
| `search.read_chunks` | action | reads a bounded page from a generation owned by this run, with complete references usable by `ai.complete` | `chunks`, `next_offset`, `more` |
| `search.enrich` | action | asks the [chat model](#ai-steps) to write context for each chunk of `generation` following `instructions`, such as which contract and clause a passage is; `scope` `chunk` (default) shows the model the chunk, `document` also the beginning of the document (`context_chars`, default 6,000). The context is searched and embedded with the chunk. `execution` and `queue` are as for `ai.complete`. Answers are cached per chunk text, instructions and context. Heavy work | `generation`, `enriched`, `cached`, `model` |
| `ai.batch.coordinate` | action | admits one named queue of parked AI requests for the installation: configurable full groups, a daily partial flush, and provider polling. Originating workflows keep their own authors. The [coordinator](#ai-steps) built-in is the caller. Output is counts only | `queue`, `submitted`, `polled`, `completed`, `released`, `busy` |
| `search.embed` | action | gives the chunks of `generation` their embedding vectors for semantic search, reusing vectors of identical chunk inputs; `mode` `immediate` (default) or `later`. Port `deferred` for `later` or a failing model, which the embedding sweep covers; without a model, `done` with nothing embedded. See [file content steps](#file-content-steps). Heavy work | `generation`, `embedded`, `reused`, `model`, `reason` |
| `search.publish` | action | makes `generation` the searchable content of the item's file (`blob_id`, default the current one); port `stale` when it is not this run's generation or the item no longer has that file. Heavy work | `published`, `generation`, `item_id`, `blob_id`, `reason` |
| `search.sync` | action | writes the [search](search.md) documents of an item or folder (`item_id`, default the run's item or the event's `resource_id`) as it is now, or removes them when it is gone; safe to run again | `written`, `removed` |
| `search.items_with_term` | action | lists, 500 at a time after `after`, the items of the workspace (or of `collection_id`) holding the term of the `term.changed` (name, labels or synonyms) or `term.merged` event that started the run; without `collection_id` it skips collections with their own `search.index` filler | `items`, `next` |

Item actions work on the run's item unless `item_id` is given. They use the same validation, permissions, revisions, audit records and events as API requests.

### Reading text

`document.read_text` reads a file's text with `internal/doctext`:

- **PDFs:** each page's text layer is read with the linked PDFium SDK. With `ocr: auto`, a page without a text layer is rendered at 200 DPI and read by [OCR](ocr.md); `always` OCRs every page; `never` reads text layers only. PaperGo's own searchable PDFs (Photos to PDF) carry their OCR text, so they are not OCRed again.
- **Images** (JPEG, PNG, WebP, and the first page of a TIFF) are turned upright and read by OCR.
- **Without OCR** configured, `auto` reads text layers only and counts the pages it could not read in `skipped_pages`; `always` fails.
- **Language:** the file's language hint (`languages` on upload, `X-OCR-Languages`) when it names one language, else the language detected among the hinted ones or `SEARCH_LANGUAGES`, else none.

The text is kept once per file bytes and reading settings (reader, OCR engine and models, mode, page bound, languages): an identical file, or the same file in a later run, reuses it without reading again (`reused`). Later steps refer to it by `text_id`. `GET /v1/items/{id}/text` returns it ([documents](documents.md)), content packages carry it with the file, and it is deleted when no file with its bytes remains.
### File content steps

`search.chunk`, `search.embed` and `search.publish` build the file content of [search](search.md#file-content) in steps people can recompose:

- **Chunkers.** `window` (default) cuts each page into windows of about `max_chars` characters (1,200) that end at word boundaries and overlap by `overlap` (150), as PaperDotNet's passages; `pages` makes a chunk per page; `paragraphs` merges paragraphs up to the size; `headings` splits at headings (numbered, `§`, or capitals) and starts each chunk with its heading; `whole` makes the whole text one chunk (up to 20,000 characters). At most `max_chunks` (400) per text.
- **Generations.** A `search.chunk` node starts a generation named by the run and the node; staging again replaces only that node's chunks, and another `search.chunk` can add to the same generation (`generation: "{step:chunk.generation}"`), such as a `summary` chunk. Nothing is searched until `search.publish`.
- **Hashes.** Each chunk's input hash covers its kind, text and enrichment, not the file's name, so renaming never makes chunks new.
- **Embedding.** `search.embed` gives a generation's chunks their vectors for [semantic search](search.md#semantic-and-hybrid-search): chunks whose input already has a vector reuse it (`reused`), the others are embedded (`embedded`). `mode: later`, or a failing model, follows `deferred` instead of failing the run, and the embedding sweep embeds the chunks once published. Without an embedding model it does nothing and follows `done`.

### AI steps

`ai.complete` and `search.enrich` call a remote chat model. Immediate execution uses any OpenAI-compatible `/v1/chat/completions` endpoint (OpenAI, Ollama, LM Studio, vLLM, llama.cpp): `AI_CHAT_PROVIDER=openai`, `AI_CHAT_MODEL`, `AI_CHAT_ENDPOINT` (default OpenAI's) and, if the server needs one, `AI_CHAT_API_KEY_FILE`. An immediate step fails when no chat model is configured.

`execution` on the node is `immediate` or `batch`. An empty value uses `AI_EXECUTION`, which defaults to `immediate`. `queue` names a batch queue; empty uses `shared`.

Batch execution needs its own provider and does not call the chat provider when that provider is missing. Set `AI_BATCH_PROVIDER=openai`, `AI_BATCH_MODEL` (empty uses the chat model), `AI_BATCH_ENDPOINT` (empty uses the chat endpoint, otherwise OpenAI's) and, when the server needs one, `AI_BATCH_API_KEY_FILE`. The client speaks the OpenAI Batch protocol, including Azure bases that end in `/openai/v1`. Requests that share a connection and an effective model go in one provider batch. A batch step follows its error port when the batch provider is not configured, when the answer does not match `output_schema`, when retries are exhausted, or when the wait reaches `AI_BATCH_DEADLINE` (default `72h`, from `1m` through `720h`).

The **AI batch coordinator** (`ai.batch`) is a system built-in in the workspace named by `AI_BATCH_WORKSPACE`. Startup turns it on there when a batch provider is configured, and leaves an existing copy, including one that was turned off, as it is. The subject that turns it on must manage that workspace, and each coordinator run needs `organization.manage`. Named queues serve the installation. The workflow that asked for the answer keeps its own author and permissions, so one workspace cannot read another's prompt or answer.

Each minute the coordinator admits its queue, including while earlier batches are still in flight. `policy` is `threshold` (full groups only), `schedule` (only on the flush, including a partial group) or `both` (default). `batch_size` sets the number of distinct uncached requests in a full group (1–100, default 20). The flush follows `flush_cron` (default `0 1 * * *`) in `time_zone` (default `Europe/Madrid`) and also sends a partial group, including a flush missed while the process was down. The coordinator polls the provider every five minutes. Identical requests share one provider line and keep separate authorization. A durable submission intent fixes batch membership before upload. After a create that might have reached the provider, reconciliation looks for that intent in provider metadata. An inconclusive or incomplete lookup never permits another create; an unresolved request reaches its deadline instead. A line that already succeeded is not sent again. Each pass handles at most one new group, one reconciliation and one poll, continuing scheduled backlogs on later passes.

`ai.complete` takes `prompt` and one text source: `input` (including a field token such as `{item:values.note}`), `text_id` (from `document.read_text`, optional `blob_id`), or `chunk` (a reference returned by `search.read_chunks`). `image` adds a file version the model sees, alone or with any text source. Text is cut at `max_chars` (default 20,000). Without `output_schema`, use `{step:ask.text}`. With an object schema, the validated answer is `{step:ask.json}`, and fields such as `{step:ask.json.total}` can feed `item.update`. Both response formats work with every text source and either execution mode. Outputs also include `cached` and `model`. Source objects have a 20 MiB read limit; batch JSONL has a 100 MiB action limit.

`search.read_chunks` reads a run-owned generation from `search.chunk`. Its bounded page contains `chunks`, each with `text` and a complete `chunk` reference, plus `next_offset` and `more`. Pass `{step:chunks.chunks.0.chunk}` to one AI step, or use `forEach` over `{step:chunks.chunks}` and pass the loop entry's `chunk`. The reference reads a complete, run-owned snapshot even when its preview was truncated or another workflow republishes search content. Snapshots are retained through the run and become collectible after it ends and their one-day lifetime passes. Chunk text is copied into the durable AI request before waiting.

`search.enrich` writes context for each chunk of `generation` following `instructions`. `scope` `chunk` (default) shows the model the chunk; `document` also shows the beginning of the document (`context_chars`, default 6,000). The context is searched and embedded with the chunk. The output is `generation`, `enriched`, `cached` and `model`. Embeddings and local decision models are separate from this remote batching.

- **Immediate requests** run at temperature 0, time out after two minutes, and are retried on 429 and 5xx, honouring `Retry-After` up to 30 seconds.
- **Cached results.** Answers are cached by the model, endpoint and request. For `ai.complete` that is the prompt, schema, input and image. For `search.enrich` it is the chunk text, instructions and context. A rerun, reindex or identical file reuses an unexpired answer. Each validated answer is saved immediately, so a failed enrichment keeps the answers of its completed chunks for retry. Concurrent misses or a crash between a provider response and saving it can repeat a request. Answers expire after the idle lifetime set by `RUN_RETENTION`. Backups keep them with the database; content packages leave them out. Batch request bodies and authoritative provider responses use durable activity records, independently of answer-cache eviction. Waits and submitted batches retain their payloads; explicit blob leases keep source bytes until consumption or cancellation.
- **Samples.** [`samples/workflows`](../samples/workflows/README.md) has two `search.content` fillers that call the model immediately: receipts with a model-written summary chunk, and contracts split at headings with context per section. [`samples/receipts-package`](../samples/receipts-package/README.md) extracts structured purchase lines from a ticket photo through batch execution and ordinary item and relationship actions.

### Reusable action results

Actions use `Tools.Results` (`internal/resultcache`) for results shared across runs. The main database's `cached_results` table stores a namespace, sharing scope, caller-defined key, opaque JSON payload, creation/use timestamps and expiry. Each namespace owns its keys and payload validation; the store accepts JSON within 1 MiB and has no action-specific columns. `Get` treats expired entries as misses and extends live entries' expiry on access, at most once a day or half their lifetime. `PutIfAbsent` atomically returns the first live payload, replacing expired entries. The retention job collects expired entries independently of run age.

`ai.complete` and `search.enrich` store answers in separate namespaces and the `installation` scope, with `RUN_RETENTION` as their idle lifetime. Their keys include model endpoint identity and request hashes. Reads and writes of a cached answer that happen inside a step use that step's transaction; provider calls stay outside it. Scope controls reuse, grants no permissions and is selected by trusted action code after authorizing its inputs. Workflow configuration and run outputs stay in their existing stores; durable integration state, revision-bound content and search vectors keep their own lifecycles.

### Staged files

Activities that produce files, such as a smaller image or a PDF composed from photos, do not replace anything themselves. They **stage** the file:

- A staged file belongs to the run and to the item it would replace.
- It records the file versions it was made from, and keeps its bytes while it is pending.
- `file.promote` makes it the item's current file. That is a new file version (`file.updated` with source `workflow` for replacements), with the item's metadata, revision history, audit and events like an upload. It succeeds only if every file it was made from is still its item's current file, so a candidate made from an old upload never replaces a newer one.
- With `recycle_sources: true` (default `false`), `file.promote` also moves the other source items to the recycle bin in the same transaction. The author must be able to write every source item. If any source changed or cannot be recycled, neither promotion nor recycling commits.
- `file.discard` drops it.

A completed or cancelled run discards its pending staged files. A failed run keeps them for a retry until the run is deleted. Purging an item deletes its staged files. The storage collector frees discarded files' bytes after its grace period.

### Reclaiming a file version

`file.reclaim` asks for one file version's bytes back. The version keeps its id, number, filename, content type, size and checksum. The request marks it `pending`. A current file, a published file, a staged file, or a byte lease held by work or a wait defers the grace period (`deferred`, with `reason` `current`, `published`, `staged` or `in use`). A retry of a version that is already pending does not move `reclaim_after`. After the one-hour object grace period, the collector marks an unprotected version `reclaimed` and deletes bytes that no other file still needs. Downloading or restoring a reclaimed version answers `410 content_unavailable`. File history keeps the version and shows it as unavailable, without download or restore. Content packages record the version without requiring its bytes. See [documents](documents.md).

### Heavy work

Every node normally runs in one write transaction. An activity that decodes, analyzes or encodes files runs in three steps instead, so it never holds the database writer while it computes:

1. It reads what it needs and checks permissions in a step transaction.
2. It does the work outside any transaction. One such work step runs at a time per server, and its result is recorded.
3. It records the result, such as a staged file, in a second step transaction.

A work step that a crash interrupts runs again; it writes no data of its own.

### Image optimization

`file.optimize_image` shrinks page images without making their text unreadable. It is PaperDotNet's storage optimization, with the same parameters and results:

1. It skips files that are not JPEG, PNG or WebP, and runs started by a file event whose file a newer one already replaced.
2. It decodes the image (at most 64 megapixels), applies its EXIF orientation, and keeps animated images unchanged.
3. A copy at most `max_analysis_dimension` pixels long (default 2600) goes to the OCR detector. Text lines that score at least `min_confidence` (default 50), are at least 6 analysis pixels high and at least 1.5 times as long as high count. Each line's height is its shortest side, projected back to the image's pixels.
4. The `percentile` (default 5) of those heights, interpolated and rounded, at least 4, is the smallest text. The scale is `target_height` (default 12) divided by it, between `minimum_scale` (default 0.05) and 1: images never grow.
5. The image is resized with the Mitchell filter (transparency flattened onto white) and encoded as WebP at `quality` (default 80).

It keeps the original, following `skipped` with the reason, when the image has no reliable text, is already WebP with its smallest text at the target, or the WebP file would not be smaller. Otherwise it stages the WebP file, named after the original, with `metrics` for reviewers: text regions, smallest text height, scale, sizes before and after, and the settings, detector and model hashes. Without an OCR engine (see [OCR configuration](ocr.md#configuration)) the node fails.

### Photos to PDF

`file.compose_pdf` ports PaperDotNet's photo to document conversion:

1. **Snapshot.** Every item of the run must be an item of the primary item's list or library that the workflow's author may write, with a JPEG, PNG or WebP file. The node records each file version; at most 100 photos.
2. **Pages.** Each photo, in the run's order, is decoded (at most 64 megapixels, not animated), turned upright and flattened onto white. Its text lines are measured as in [image optimization](#image-optimization), and the page is scaled so that its smallest line is `target_height` pixels high (default 32). Unlike PaperDotNet, pages never grow; a page without text keeps its size.
3. **Text.** OCR reads the full-resolution photo. Each line is placed, invisible, at its position on the scaled page, so the PDF can be searched and its text selected and copied. The text layer embeds a placeholder font and maps characters to Unicode, so it needs no system fonts and keeps every script.
4. **PDF.** Each page image is embedded at 150 pixels per inch, as JPEG at `quality` (default 80), or losslessly when that is smaller. The PDF is staged for the primary item, named after its file, with the other items as `others`.

The node fails if a photo changes before the PDF is staged; promotion fails if one changes before promotion commits. Either way, start a new conversion. The Photos to PDF built-in promotes with `recycle_sources: true`, so the PDF replacement and recycling commit together and no upload can arrive between them.

## Forms

Launch inputs, built-in parameters and activity settings are described by **forms**: JSON Schema objects (a draft-07 subset). A UI renders them with any JSON Schema form library, but the server never trusts a form. It applies defaults and validates every value itself.

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
- A manual start is refused before any run starts, and the run keeps the final values: `inputs` in the run detail, `{input:...}` in tokens.

**Domain pickers.** `x-papergo` on a string property (one value) or an array of strings (several, at most 100) makes it a picker of PaperGo objects. Its values are IDs, which the server checks against the data and the permissions of the person who submits the form. A required picker must not be empty.

| `kind` | Picks | Options |
| --- | --- | --- |
| `item` | items of the workspace the person may read | `collection_id`: only that list or library |
| `collection` | lists and libraries of the workspace the person may read | |
| `relationship` | items to use with a relationship type; no link is created, nodes decide | `relationship_type_id` (required) |
| `terms` | organization taxonomy terms, active and unmerged; `select: "sets"` picks term sets | `group_id` narrows either selection; `term_set_id` and `term_ids` narrow terms only |
| `keywords` | organization keywords, not deprecated: terms of its keywords set and terms available as keywords | |
| `people` | principal subjects that have access to the workspace | `access`: `read` (default), `read_draft`, `write`, `publish` or `manage` |

Set selection uses `{"kind":"terms","select":"sets"}` and browses `GET /v1/term-sets`, returning term-set IDs. Term selection returns term IDs. Term pickers browse `GET /v1/term-sets/{id}/terms`; keyword pickers suggest from `GET /v1/keywords?q=` and add new keywords with `POST /v1/keywords` before the form is submitted (see [taxonomy](application-foundation.md#controlled-taxonomy)).

Saving a workflow checks that collections and relationship types belong to the workspace, and that taxonomy pickers reference existing organization groups, sets and terms. Taxonomy pickers require `taxonomy.read`.

**Selection hints.** The root of a [selection](#selection-runs) workflow's launch form can carry `x-papergo-selection`, text hints for the screen that chooses and orders the items: `preview` (such as `image`), `item_label` (such as `page`), `order_label` and `primary_description`. They are presentation only.

[Task](#tasks) answers fill in forms with the same renderer, defaults and checks, with the pickers checked against the person who answers.

## Tasks

A task asks people to do something and say so: approve, confirm data, fill in a form. Workflows create tasks with the `task` and `approval` nodes; people create them with the API and the web app's Tasks screen. An approval is a task of kind `approval`. The model and its rules live in `internal/tasks`, shared by workflows, the API and PaperGo itself.

The Tasks screen offers Waiting for me, Approvals, Completed and All views. **Load more** appends the next page of the selected view while keeping the current task open.

```json
"check": {"activity": "task", "inputs": {
  "title": "Check the invoice data of {item:name}",
  "assignees": ["role:members"],
  "key": "invoice_check",
  "outcomes": [
    {"key": "correct", "label": "Correct", "style": "primary"},
    {"key": "fix", "label": "Needs a fix", "comment": "required", "skip_form": true},
    {"key": "not_invoice", "label": "Not an invoice", "style": "danger", "skip_form": true}],
  "input_schema": {"type": "object", "required": ["total"], "properties": {"total": {"type": "number", "title": "Total"}}},
  "values": {"total": "{item:values.total}"},
  "due": "48h"},
  "next": {"correct": "save", "fix": "notify_uploader", "not_invoice": "move"}}
```

- **Assignees** are recipient entries:
  - a person's subject;
  - `group:<id>`, nested groups included;
  - `role:owners` or `role:members` of the workspace;
  - `creator` (the item's creator), `actor` (who caused the run), or `field:<key>` (the people in a field of the item);
  - tokens.

  No assignees means the workspace's owners. Unknown people or groups fail the node with their names, and inactive users are left out. At most 50.
- **Access.** Assignees who are people must be able to read the workspace and, for a task about an item, the item's draft. This is checked when the task is created (the node fails, naming who cannot) and again when someone answers; a group's members are checked when they answer. A task is visible to its assignees, its creator and the workspace's owners.
- **Outcomes** are the answers, in order (default one, `done`), at most 10. Each has a `key` (a lowercase name other than `error` and `expired`), a `label`, a `style` for its button (`primary`, `danger`, `default`), and:
  - `comment`: `none`, `optional` (default) or `required`;
  - `skip_form`: answers without the form, such as a rejection;
  - `allow_stale`: allows the answer when the review's staged file was made from files that are no longer current;
  - `decisive`: in `all` mode, completes the task at once.
- **Ports.** A task node's ports are its outcomes, plus `done` and `error`. Outcomes must be a literal list, so saving checks `next` against them. An outcome without a port ends the run.
- **Completion.** `any` (default): the first answer completes the task. `all`: every assignee answers once, a group by any one of its members; someone assigned by name and through a group answers once for each. A decisive outcome completes it at once; otherwise it completes when everyone has answered, with the outcome chosen most often (ties go to the outcome listed first). The output's `completed_by`, `comment` and `input` are the completing answer's: the latest answer with that outcome. Every answer is in the output's `responses`.
- **Reassigning.** When an assignee can no longer answer, for example after leaving, a workspace owner (or the creator of a person's task) replaces the open task's assignees, as entries like those of a new task. Answers of entries that stay count; with `all`, the task completes once every entry left has answered, and a waiting run continues.
- **Form.** `input_schema` is a [form](#forms), frozen on the task when it is created, so editing the workflow does not change an open task. Its pickers must point at the workspace, as a launch form's do; this is checked when the workflow is saved and when the task is created. `values` prefill it; tokens in them, such as `{item:values.total}`, are resolved then, and they must be valid answers, picks included. An answer sends the form's values unless its outcome skips the form; the server applies defaults and checks them, and pickers are checked against the person who answers. Later nodes read them as `{step:check.input.total}`.
- **Review.** `review` shows a staged file of the run beside the form: `file.compare` shows the item's current file beside a staged image, both at 100%; `file.preview` shows a staged document, such as a PDF. An answer whose outcome does not `allow_stale` fails with 409 when the staged file was made from a file that is no longer current.
- **Waiting.** With `wait` (the default), the run parks on a [durable wait](#durable-waits): it shows as `waiting`, holds no worker, and resumes moments after the task completes. Waiting runs count as active for [concurrency](#concurrency) and are not deleted by run retention. Cancelling a waiting run, replacing it, or deleting its item cancels the task and closes the wait; a run cancelled after its task completed stays cancelled even if it was about to resume. With `wait: false`, the node creates the task, outputs `task_id` and continues at `done`; the task outlives the run, and other workflows react to it with `task.*` triggers.
- **Other inputs:** `key` (a lowercase name triggers and lists filter on), `priority` (`low`, `normal`, `high`), `due` (a duration such as `48h`, a date or a time), `item_id` (default the run's item).

Answers and their form values stay on the task after it completes, and completed tasks are kept when their runs are deleted. Creating and answering tasks are recorded in the workspace's audit trail (`task.create`, `task.respond`).

## Durable waits

A node that must wait for something outside the run creates a durable wait. Human tasks and external activities, including batch AI requests, use the same completion contract. The run shows as `waiting`; DBOS `Recv` persists its suspension and timeout, while DBOS `Send` durably delivers the completion. Publication and its application marker commit in one database transaction, with an idempotency key. Messages sent before the receiver starts remain in its inbox. Waiting does not consume a heavy-work slot, and restarting replays DBOS checkpoints. Cancellation closes the waits and releases payload references, byte leases and executor claims.

A completion envelope has `output` (the action's data), optional `port` and optional `error`. An error enters the node's retry and error-port handling. A registered activity's `Resume` handler can validate current permissions, transform the result, or start another work or wait phase. Deadlines complete through the same contract. A task's `due_at` describes its due date; it does not implicitly cancel the task.

Extensions list, claim, update and complete open waits through the [wait routes](#api). A `workspace_id` limits the call to a workspace the caller manages. Omitting it requires `organization.manage`. Lists support named `queue` filtering and cursor pagination. Claims expire after ten minutes; updates by claim holders require their current, unexpired token. Completion is an administrative operation under the same scope check.

Wait data is bounded at 16 KiB and completion at 64 KiB. Large inputs belong in `Tools.Records` (`internal/activitystore`), whose namespace/scope/key records have explicit expiry and retention references. Expiry cannot delete a record while an active consumer references it. `Park.PayloadID` retains an activity record; `Park.BlobIDs` acquires source-file leases. Immediate heavy work uses `Prepared.BlobIDs` for the same byte protection. Consumption, terminal runs and cancellation release these references. A deferred reclamation request is revisited automatically and starts its grace period once its last protection disappears.

The AI module stores requests until their batch deadline, completed provider responses while their requests need them, and scheduler/submission state separately from waits. Coordinator leases have expiry and fencing tokens; terminal holders can be replaced. Completed batches retire into expiring records, and batches with no remaining consumers release their payload references. Pending submission records also have a bounded lifetime beyond consumer deadlines, so disabling a coordinator or changing providers cannot retain them forever. Result-cache lifetime is solely a reuse policy.

See [workflow extensions](workflow-extensions.md) for the registration and durability boundaries.

## Selection runs

A manual trigger with `"selection": "selection"` starts one run over several items. `POST /v1/workflows/{id}/runs` then takes:

- `item_ids`: 1 to 100 items of the workflow's workspace, limited to the trigger's collection when configured. Duplicates are dropped and the first occurrence keeps its place.
- `primary_item_id`: optional, one of `item_ids`. It defaults to the first.

Every item needs the starter's `write` access and must meet the condition before the run starts.

The primary item is the run's item: actions without `item_id`, `{item:...}` tokens and the run's events use it. `{run:items}` lists all members in order, so nodes reach the others with `"item_id": "{run:items.1}"`.

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
to inspect node results and retry failed runs. General run listing, execution
details, cancellation and retry require workspace manage access.

## Who a run acts as

A run acts as the **author of its workflow version**: the subject who saved that version, or who configured the built-in. Every step checks that author's permissions, as an API request would. A workflow therefore never does more than its author may, and a run fails visibly if the author loses access. `{trigger:actor}` and `{run:actor}` still name who caused the run.

Changes a run makes raise events like any other change, one level deeper than the event that started the run. Events at depth 5 start nothing, which stops workflows that keep triggering each other.

## Built-in workflows

Built-in workflows are processes PaperGo ships, in the same model.

| Key | Name | Scope | Parameters |
| --- | --- | --- | --- |
| `documents.optimize` | Optimize image storage: when an image is added (uploaded, through WebDAV or restored), or on demand, `file.optimize_image` proposes a smaller copy and an `approval` asks the `approvers` to compare it with the current file at full size; approving promotes it as a new file version, rejecting discards it. `concurrency: replace`, so a newer file cancels a waiting review. | per library | `target_height`, `min_confidence`, `percentile`, `max_analysis_dimension`, `minimum_scale`, `quality` (see [image optimization](#image-optimization)), `approvers` (people; none means the workspace's owners) |
| `documents.photos_to_pdf` | Photos to PDF: started on a selection (`selection: "selection"`, launch screen with image previews and a page order), `file.compose_pdf` combines the photos into one searchable PDF and an `approval` asks the `approvers` to review it (`file.preview`). Approving atomically promotes it as the primary item's new file version, keeping its metadata and history, and sends the other photos to the recycle bin; rejecting discards it. `concurrency: skip`, so a second conversion with any of the same photos is not started while one waits. | per library | `target_height` (default 32), `quality` (default 80), `approvers` (people; none means the workspace's owners) |
| `search.content` | Index file content: when a PDF or an image is added (also through WebDAV, a restore, a workflow or a content reindex), `document.read_text`, `search.chunk`, `search.embed` and `search.publish` make its text searchable with pages and, with an embedding model, by meaning ([search](search.md#file-content)). `concurrency: coalesce`. A system built-in filling the `search.content` role, on by default; outputs `blob_id`, `text_id`, `pages`, `language`, `generation`, `chunks` | role (workspace, or per list or library) | `ocr`, `max_pages`, `chunker`, `max_chars`, `overlap`, `max_chunks`, `embed` |
| `search.index` | Index for search: on item and folder changes, `permissions.changed` (`replace`, `move`), `term.changed`, `term.merged` and manual starts, `search.sync` rewrites the entry's [search](search.md) documents; term events reindex every item holding the term. `concurrency: coalesce`. A system built-in filling the `search.index` role, on by default. | role (workspace, or per list or library) | none |
| `items.expire` | Unpublish expired items: every day, unpublishes the published items whose date `field` is before today | per list or library | `field` (an indexed date field), `cron` (default `0 1 * * *`), `time_zone` (default `UTC`) |
| `ai.batch` | AI batch coordinator: each minute, admits one named queue of parked AI requests for the installation. Full groups of `batch_size` go on every pass when the policy includes a threshold; the daily flush also sends a partial group; in-flight batches are polled. Originating workflows keep their own authors. A system built-in. Startup turns it on in `AI_BATCH_WORKSPACE` when a batch provider is configured, and leaves an existing copy as it is. See [AI steps](#ai-steps) | one workspace | `batch_size` (1–100, default 20), `queue` (default `shared`), `policy` (`threshold`, `schedule` or `both`, default `both`), `flush_cron` (default `0 1 * * *`), `time_zone` (default `Europe/Madrid`) |

How people use them:

- **Turn on, off, and set parameters:** `PUT /v1/workspaces/{id}/workflow-builtins/{key}` with `{"collection_id", "enabled", "parameters"}`. Each built-in describes its parameters as a form (`parameters_schema` in the catalog); they get its defaults and checks.
  - The first call creates the built-in's workflow. Later calls need its ETag in `If-Match`.
  - The workflow's definition is the release definition with the parameters filled in. Its runs, versions and history work like any workflow's.
- **Edit freely:** a built-in's definition cannot be edited in place. `POST .../workflow-builtins/{key}/copy` creates an ordinary workflow from it and turns the built-in off where it was on, so the copy replaces it.
- **Release updates:** when PaperGo starts, every built-in workflow is brought to the running release, and a changed release definition becomes a new version. A built-in no longer shipped, or whose parameters no longer validate, is turned off.

### Roles

Some processes are **roles**: product processes that one workflow fills, which people can turn off or replace with their own process. The catalog lists them in `roles`, each with the `output_schema` its fillers return.

- **Fillers.** A role's built-in fills it by default. A copy of the built-in fills it too, and so does any workflow created with `"role": "<role>"`. A filler declares outputs compatible with the role's `output_schema` (the same names and types, and every required one), which saving checks. Workflows that follow a role trigger on `role.{role}.completed` (or `failed`), which every filler raises with its outputs, whichever fills the role for the item's collection.
- **Scopes.** A filler fills its role for the whole workspace or, created with `collection_id`, for one list or library. For an item, a folder or a file of a collection, the collection's own filler runs instead of the workspace's; a collection whose own filler is turned off has the role off. `PUT .../workflow-builtins/{key}` with `collection_id` gives a collection its own copy of a role built-in, on or off.
- **One per scope.** Turning a filler on, or creating one enabled, turns the other enabled fillers of the role in the same scope off.
- **Defaults.** Default role built-ins are turned on in every workspace, by the workspace's creator, before its first change is dispatched. A built-in people turned off, copied, replaced or deleted stays as they left it.
- **What fills a collection's role:** `GET /v1/resources/{id}/workflow-roles` lists each role with the workflow that fills it for the list or library, its `scope` (`collection` or `workspace`) and whether it is `enabled`.
- **System built-ins** (`system` in the catalog) are product processes. Their runs use a queue of their own, so they never delay people's workflows; their successful runs are kept a day. Their release definitions receive `term.changed` whatever their author's scopes, because they never expose its data.

## Runs

| Status | Meaning |
| --- | --- |
| `queued` | waiting for a worker |
| `running` | executing, or waiting in `delay` |
| `waiting` | waiting for a task or another durable wait, without holding a worker |
| `completed` | finished |
| `failed` | a node failed with no `error` port, or `fail` ran |
| `cancelled` | stopped by a manager |

`GET /v1/workflow-runs/{id}` shows the run's result (its status, last node, variables and [outputs](#outputs)) and its recorded steps: `load`, each node attempt, and `finish`, each with output or error.

- **Retry** (`POST .../retry`) runs a failed run again from the failed step as a new run with `retry_of`. Steps before it keep their recorded results, so nothing they wrote is written twice.
- **Cancel** (`POST .../cancel`) stops a queued, running or waiting run. A waiting run's tasks are cancelled, its waits are closed and claims it holds are released.

**How runs start.** A change and its events commit together in the domain event log (`domain_events`), so a change that rolls back raises nothing, and the write does no workflow work. A dispatcher in the runner takes logged events in order, matches them to workflows, and queues the runs. The runs and the events' dispatched mark commit together, and each event starts at most one run per workflow, so a crash dispatches events again without duplicates. Queued runs execute on whichever server's workers take them. The dispatcher wakes when a change commits and also checks every second, so it picks up events that other servers or a restart left behind.

A manual start returns run IDs at once. Until its event is dispatched, the run shows as `queued`.

Run lists leave out the runs of system built-ins, which run on every change, unless `workflow_id` selects their workflow or `item_id` selects the item being inspected.

Finished runs, and dispatched events, are deleted after `RUN_RETENTION` (default 30 days). Deleting a workflow stops it from matching triggers; its versions and runs are kept, and running runs finish with their version.

## Permissions

| Action | Needs |
| --- | --- |
| List and read workflows | workspace `read` |
| Create, change, delete workflows; turn on, configure and copy built-ins | workspace `manage` (built-ins per collection also need collection `manage`) |
| Start a manual workflow on items | `write` on each item |
| Start a manual workflow with no item | workspace `manage` |
| See, cancel and retry runs | workspace `manage` |
| List, claim, update and complete waits of one workspace | workspace `manage` |
| List, claim, update and complete waits with no workspace | `organization.manage` |
| Answer a task | being an assignee (or in an assigned group) who can read the workspace and the item's draft |
| Create a task | workspace `read`, and `write` on the item when it concerns one |
| Cancel a task | its creator or a workspace owner; a workflow's task ends with its run |
| See a task | being an assignee, its creator or a workspace owner |

## API

| Method | Route | Purpose |
| --- | --- | --- |
| GET | /v1/workflow-catalog | Trigger types, activities, built-ins |
| GET / POST | /v1/workspaces/{id}/workflows | List or create workflows |
| GET / PUT / DELETE | /v1/workflows/{id} | Read, replace (If-Match), delete (If-Match) |
| GET | /v1/workflows/{id}/versions | Versions, newest first |
| POST | /v1/workflows/{id}/runs | Manual start: `{"item_ids": [...], "inputs": {...}}` (202 with `run_ids`) |
| GET | /v1/workspaces/{id}/workflow-builtins | Built-ins with the workspace's built-in workflows |
| PUT | /v1/workspaces/{id}/workflow-builtins/{key} | Turn on/off, set parameters |
| POST | /v1/workspaces/{id}/workflow-builtins/{key}/copy | Copy into an editable workflow |
| GET | /v1/workspaces/{id}/workflow-runs | Runs, newest first (`workflow_id`, `item_id`, `$skiptoken`, `$top`) |
| GET | /v1/workflow-runs/{id} | A run with result and steps |
| POST | /v1/workflow-runs/{id}/cancel, /retry | Cancel, or retry a failed run |
| GET | /v1/workflow-waits | Open waits of one `kind` (`queue`, `workspace_id`, `$top` at most 100, `$skiptoken`) |
| POST | /v1/workflow-waits/claim | Claim open waits for `holder`; the body is `kind`, optional `key` and `workspace_id`, and `limit`. The response is `value` and `claim_token`; each wait includes `claim_until` |
| PATCH | /v1/workflow-waits | Replace one open wait's `data`, as its owning run or as `holder` with `claim_token` |
| POST | /v1/workflow-waits/complete | Complete one open wait with `completion`, an envelope with `output`, `port` or `error` |
| GET | /v1/me/tasks | The caller's tasks (`waiting=true` for those still to answer, `status`, `kind`, `key`, `workspace_id`, `item_id`, `$top`); open ones by due time, in pages linked by `@odata.nextLink` like the other task lists |
| GET / POST | /v1/workspaces/{id}/tasks | A workspace's tasks, for its owners; create a task: `{"title", "description", "assignees", "item_id", "kind", "key", "completion", "outcomes", "input_schema", "values", "priority", "due_at"}` |
| GET | /v1/items/{id}/tasks | The tasks about an item the caller may see |
| GET | /v1/tasks/{id} | A task with its answers, `can_respond` and `answered` |
| POST | /v1/tasks/{id}/responses | Answer: `{"outcome", "comment", "input"}` |
| POST | /v1/tasks/{id}/reassign | Replace an open task's `assignees` (owners, and the creator of a person's task) |
| POST | /v1/tasks/{id}/cancel | Cancel a person's or the system's open task |
| GET | /v1/staged-files/{id}, /content | A staged file and its bytes, for readers of its item |

## Runtime

The runner ([DBOS Transact Go](https://github.com/dbos-inc/dbos-transact-golang)) runs inside the API process and keeps its state in the PaperGo SQLite file:

- **Reserved table names.** DBOS creates and migrates its own tables when the API starts. Their names are reserved for PaperGo tables: `application_versions`, `dbos_migrations`, `event_dispatch_kv`, `notifications`, `operation_outputs`, `queues`, `streams`, `workflow_events`, `workflow_events_history`, `workflow_input`, `workflow_output`, `workflow_schedules`, `workflow_status`.
- **Write locking.** Write transactions lock when they begin (`_txlock=immediate`) and wait up to 30 seconds for the lock, so requests and workflow steps share the single SQLite writer.

Configuration:

| Setting | Meaning |
| --- | --- |
| `NODE_ID` | This process's identity (default `local`); a restarted process recovers the runs it was executing |
| `RUN_RETENTION` | How long finished runs are kept (default `720h`) |

## Building features on workflows

PaperGo code that should happen after a change or on a schedule:

1. Adds an activity in `internal/workflow`, documented in the catalog and safe to run once per step. Activities change data through `dms` with the run's author, so permissions, validation and events stay in one place.
2. Ships the process as a built-in workflow when people should see or change it.
3. Never reacts to changes in hidden code paths.

Relationship creation, attribute updates, deletion and taxonomy merges emit `item.updated` for each live endpoint. These writes advance both item ETags and modification timestamps without adding metadata history. Their data includes `relationship_id`, `relationship_action`, `peer_id`, `changed_fields: ["$relationships"]`, the head `revision_id` and `content_type_id`. Term filters can match present terms; added/removed term filters do not fire for relationship-only changes. Duplicate link requests leave both endpoints unchanged. Purging an item emits relationship changes for surviving peers.

`item.update` also accepts optional `expected_version`, `expected_blob_id` and `expected_taxonomy` preconditions. They are checked with the write in one transaction; a failed guard follows `error` without mutation. See [guarded decision consumption](decisions.md#guarded-review-and-writes).
