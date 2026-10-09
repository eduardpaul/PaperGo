# Workflows

Workflows are how PaperGo does anything that happens **after** a change or **on a schedule**. A workflow is data that workspace managers can read, change, turn off or replace. That includes the processes PaperGo ships, which are *built-in workflows* rather than hidden code. New features that react to changes are delivered as workflow activities and built-in workflows.

A workflow has:

- **triggers**: what starts a run, such as an item being created, a cron schedule, a manual start, or another workflow's event;
- an optional **condition**: a query filter the run's item must meet;
- a **flow**: nodes that each run one **activity**, connected by outcome **ports**.

Every save that changes the definition creates a new immutable **version**. A run always executes the version it started with.

Runs are durable. Each node runs as one step that commits exactly once. A run continues at the node it reached after a crash or restart, and waits such as `delay` survive restarts. The engine is described in the [runner plan](plans/runner.md).

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

## Triggers

| Type | Starts a run when | The run's item |
| --- | --- | --- |
| `item.created` | an item is created (its first revision) | the item |
| `item.updated` | an item's content changes (a new head revision) | the item |
| `item.published` | a revision is published, explicitly or automatically | the item |
| `item.unpublished` | an item is unpublished | the item |
| `item.deleted` | an item is deleted, alone or with its folder | none (`{trigger:resource_id}` names it) |
| `schedule` | a cron occurrence: `cron` (5 fields) and `time_zone` (IANA, default UTC) | see below |
| `manual` | someone starts it through the API | each chosen item, the selection's primary item, or none |
| `wf.{key}.{event}` | a run of workflow `key` raises `event`; `completed` and `failed` are raised when a run ends | that run's item |

Fields of a trigger:

- `collection_id` limits item and workflow-event triggers to one list or library.
- `content_type_id` (with `collection_id`) limits item triggers to one content type.
- For a schedule:
  - **With `collection_id`:** each occurrence starts one run per item of the collection that meets the condition, up to 500, on `surface` (`head` by default, or `published`).
  - **Without `collection_id`:** one run with no item.
  - Occurrences missed while PaperGo was down start once.
  - A change to a workflow never starts runs for times already past.
- A `manual` trigger with `collection_id` starts only on items of that collection. Without it, it starts with no item. Manual workflows can declare a launch form, `input_schema` (see [Forms](#forms)).
- `selection` on a `manual` trigger with `collection_id`:
  - `per_item` (the default) starts one run per chosen item.
  - `selection` starts **one run for all chosen items**, in order, for work that combines them, such as composing one document from several photos. See [selection runs](#selection-runs).

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
- `item.published`: `revision_id`, `revision_number`, `publication_id`
- `item.unpublished`: `revision_id`
- `schedule`: `workflow_id`, `occurrence`
- `manual`: `workflow_id`, `inputs`
- `wf.*`: `run_id` and the raised data; `completed`/`failed` add `status`, `node` and, on failure, `error`

Only items raise item events. Folders, collections and settings do not.

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

Node names use letters, digits, spaces, `_` and `-`. Every node must be reachable from `start`. Loops are allowed: a run visits at most 1,000 nodes, and a node's output is limited to 64 KB.

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

## Activities

`GET /v1/workflow-catalog` lists every activity with its ports, an `input_schema` describing its node inputs and an `output_schema` describing what `{step:node...}` can read. Both are [forms](#forms), so an editor can render a node's settings without knowing the activity. Node inputs may be tokens, so saving checks only that required inputs are present and every input is known; types are checked when the node runs.

| Activity | Kind | Does | Output |
| --- | --- | --- | --- |
| `if` | flow | `filter` (a query filter on the item's head), or `left` `op` `right` (`eq ne gt ge lt le contains empty not_empty`; numbers compare as numbers); ports `true`/`false` | `result` |
| `set_variable` | flow | sets `name` to `value` | `name`, `value` |
| `delay` | flow | waits `duration` (`90m`, `48h`) or `until` a date or time, durably | `until` |
| `event.raise` | flow | raises `wf.{key}.{event}` with `data` and the run's item | `event` |
| `end` / `fail` | flow | completes the run / fails it with `message` | |
| `item.get` | action | reads an item | the item |
| `item.update` | action | sets `name`, `tags`, and `values` (merged; `null` removes a value) | the item |
| `item.create` | action | creates an item in `collection_id` (optional `parent_id`, `content_type_id`, `tags`, `values`) | `item_id`, `item` |
| `items.query` | action | queries `collection_id` with `filter`, `sort`, `limit` (≤ 100), on the head surface | `items`, `count` |
| `item.publish` | action | publishes the head revision, if not already published | `published` |
| `item.unpublish` | action | unpublishes, if published | `unpublished` |
| `item.delete` | action | deletes the item | `deleted` |

Item actions work on the run's item unless `item_id` is given. They use the same validation, permissions, revisions, audit records and events as API requests.

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
- Structure: `type` (`string`, `number`, `integer`, `boolean`, `array`, `object`), `properties`, `required`, `items`.
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
| `terms` | terms of the workspace's taxonomy, not deprecated | `group_id`, `term_set_id`, `term_ids` (only those terms) |
| `keywords` | keywords of the workspace, not deprecated: terms of its keywords set and terms available as keywords | |
| `people` | principal subjects that have access to the workspace | `access`: `read` (default), `read_draft`, `write`, `publish` or `manage` |

Term pickers browse `GET /v1/term-sets/{id}/terms`; keyword pickers suggest from `GET /v1/workspaces/{id}/keywords?q=` and add new keywords with `POST /v1/workspaces/{id}/keywords` before the form is submitted (see [taxonomy](application-foundation.md#controlled-taxonomy)).

Saving a workflow checks that the collections, relationship types, term groups, term sets and terms its pickers name belong to the workspace.

**Selection hints.** The root of a [selection](#selection-runs) workflow's launch form can carry `x-papergo-selection`, text hints for the screen that chooses and orders the items: `preview` (such as `image`), `item_label` (such as `page`), `order_label` and `primary_description`. They are presentation only.

Approval steps (planned) will collect their answers with the same forms and checks.

## Selection runs

A manual trigger with `"selection": "selection"` starts one run over several items. `POST /v1/workflows/{id}/runs` then takes:

- `item_ids`: 1 to 100 items of the trigger's collection. Duplicates are dropped and the first occurrence keeps its place.
- `primary_item_id`: optional, one of `item_ids`. It defaults to the first.

Every item needs the starter's `write` access and must meet the condition before the run starts.

The primary item is the run's item: actions without `item_id`, `{item:...}` tokens and the run's events use it. `{run:items}` lists all members in order, so nodes reach the others with `"item_id": "{run:items.1}"`.

The membership is recorded with the run (`items` in the run API) and does not change:

- **Retries.** A retried run keeps the same members.
- **Run listing.** Listing runs with `item_id` includes selection runs the item is a member of.
- **Deleting a member.** Deleting a member, alone or with its folder, cancels the queued or running selection runs it belongs to. The exception is the run that deleted it, for example a run that replaces several items with one.

## Who a run acts as

A run acts as the **author of its workflow version**: the subject who saved that version, or who configured the built-in. Every step checks that author's permissions, as an API request would. A workflow therefore never does more than its author may, and a run fails visibly if the author loses access. `{trigger:actor}` and `{run:actor}` still name who caused the run.

Changes a run makes raise events like any other change, one level deeper than the event that started the run. Events at depth 5 start nothing, which stops workflows that keep triggering each other.

## Built-in workflows

Built-in workflows are processes PaperGo ships, in the same model.

| Key | Name | Scope | Parameters |
| --- | --- | --- | --- |
| `items.expire` | Unpublish expired items: every day, unpublishes the published items whose date `field` is before today | per list or library | `field` (an indexed date field), `cron` (default `0 1 * * *`), `time_zone` (default `UTC`) |

How people use them:

- **Turn on, off, and set parameters:** `PUT /v1/workspaces/{id}/workflow-builtins/{key}` with `{"collection_id", "enabled", "parameters"}`. Each built-in describes its parameters as a form (`parameters_schema` in the catalog); they get its defaults and checks.
  - The first call creates the built-in's workflow. Later calls need its ETag in `If-Match`.
  - The workflow's definition is the release definition with the parameters filled in. Its runs, versions and history work like any workflow's.
- **Edit freely:** a built-in's definition cannot be edited in place. `POST .../workflow-builtins/{key}/copy` creates an ordinary workflow from it and turns the built-in off where it was on, so the copy replaces it.
- **Release updates:** when PaperGo starts, every built-in workflow is brought to the running release, and a changed release definition becomes a new version. A built-in no longer shipped, or whose parameters no longer validate, is turned off.

## Runs

| Status | Meaning |
| --- | --- |
| `queued` | waiting for a worker |
| `running` | executing, or waiting in `delay` |
| `completed` | finished |
| `failed` | a node failed with no `error` port, or `fail` ran |
| `cancelled` | stopped by a manager |

`GET /v1/workflow-runs/{id}` shows the run's result and its recorded steps: `load`, each node attempt, and `finish`, each with output or error.

- **Retry** (`POST .../retry`) runs a failed run again from the failed step as a new run with `retry_of`. Steps before it keep their recorded results, so nothing they wrote is written twice.
- **Cancel** (`POST .../cancel`) stops a queued or running run.

**How runs start.** A change and its events commit together in the domain event log (`domain_events`), so a change that rolls back raises nothing, and the write does no workflow work. A dispatcher in the runner takes logged events in order, matches them to workflows, and queues the runs. The runs and the events' dispatched mark commit together, and each event starts at most one run per workflow, so a crash dispatches events again without duplicates. Queued runs execute on whichever server's workers take them. The dispatcher wakes when a change commits and also checks every second, so it picks up events that other servers or a restart left behind.

A manual start returns run IDs at once. Until its event is dispatched, the run shows as `queued`.

Finished runs, and dispatched events, are deleted after `RUN_RETENTION` (default 30 days). Deleting a workflow stops it from matching triggers; its versions and runs are kept, and running runs finish with their version.

## Permissions

| Action | Needs |
| --- | --- |
| List and read workflows | workspace `read` |
| Create, change, delete workflows; turn on, configure and copy built-ins | workspace `manage` (built-ins per collection also need collection `manage`) |
| Start a manual workflow on items | `write` on each item |
| Start a manual workflow with no item | workspace `manage` |
| See, cancel and retry runs | workspace `manage` |

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
| GET | /v1/workspaces/{id}/workflow-runs | Runs, newest first (`workflow_id`, `item_id`, `after`, `limit`) |
| GET | /v1/workflow-runs/{id} | A run with result and steps |
| POST | /v1/workflow-runs/{id}/cancel, /retry | Cancel, or retry a failed run |

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
