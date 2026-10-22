# Auto-filing: taxonomy fields filled when a file is added (plan)

Status: **proposed.** Nothing in this plan is implemented.

## Outcome

When someone adds a document to a library, PaperGo fills that library's **empty** taxonomy fields (Document type, Department, Period, …) from the document's text. It runs locally and is on by default, with no setup:

- a confident choice is written;
- an uncertain choice is put to one person in one approval task;
- a value a person chose is never overwritten.

## Design

Auto-filing is an ordinary built-in workflow that fills a role. That makes it flexible and portable without new mechanisms:

- **Flexible.** Managers turn it off per library, change its parameters, copy it to customise the script, or replace it with their own workflow that fills the same role (rules, a chat model, anything). Workflows that follow `role.file.autofile.completed` keep working whichever workflow fills it.
- **Portable.** Its configuration is the workflow's parameters, which [workspace packages](../workspace-portability.md) already carry and remap. Parameters hold only field keys, fixed values and assignee selectors. Quality depends only on term descriptions, which travel with taxonomy, so an imported workspace files the same way. It keeps no learned or per-installation state.

No core table, DAL or core API change is needed.

## What ships

### 1. Excerpt option for decisions

`POST /v1/ai/taxonomy-decisions` and `POST /v1/ai/decisions` take `max_tokens` (64–896) with `text_id`. The decision reads only the head of the stored text, so it fits the model's context. The source guard (`source.item_version`) and the cache key are kept, and the cache key includes `max_tokens` (`internal/aiops/decide.go`).

Without this option, a document longer than about 1,024 tokens fails with "split the document". That covers most real files.

### 2. Role and built-in

`internal/aiops` registers role `file.autofile` and built-in `documents.autofile` (`Role`, `Default: true`) **only when the decisions engine is configured**. This follows how `documentpreview` and `documentsearch` register theirs. A deployment without the Laya model has no auto-filing and nothing broken.

- **Triggers:** `file.added`, `file.updated`, and manual runs per item.
- **Concurrency:** `replace`. A newer file cancels an older filing and its pending task.
- **Script** (`builtin/documents.autofile.ts`):
  1. Reads the text (`POST /v1/items/{id}/text`, `ocr: auto`). Without text it ends with `skipped`.
  2. For each eligible field, chooses one level at a time with `selection: single`. The candidates are the active children of the current term, the roots to start with, at most 19 per level. A choice above the apply threshold is kept and the run goes one level deeper. A choice between the suggest and apply thresholds becomes a suggestion and stops the descent. Anything lower stops it.

     A field is eligible when it is a term field, is listed in `fields` (or `fields` is empty), and is empty on the item.
  3. Writes every kept term in one `PATCH` with `If-Match` set to the version the decision read. If the item changed meanwhile, it writes nothing and ends with `skipped`.
  4. Puts the suggestions in one approval task for `reviewers`, with a prefilled term picker per field, as the [taxonomy-review sample](../../samples/workflows/taxonomy-review.ts) does. Approving writes them under the same version rule.
- **Output schema** (the role's contract), keyed by field key:
  - `applied`: field key → term IDs;
  - `suggested`: field key → term IDs;
  - `skipped`: field key → reason.

  Reasons are a fixed set, as Go constants: `has_value`, `no_terms`, `too_many_terms`, `no_text`, `changed`.

### 3. Parameters

| Parameter | Default | Values |
| --- | --- | --- |
| `fields` | `[]` (every term field) | field keys |
| `confidence` | `cautious` | `cautious` (apply ≥ 0.9, suggest ≥ 0.7), `balanced` (0.8 / 0.6), `eager` (0.7 / 0.55) |
| `reviewers` | `["actor"]` (who added the file; none means the owners) | assignee selectors: `actor`, `creator`, `role:owners`, `field:<key>`, people |

Fields with several values get the single best term. Item keywords are not filled.

### 4. Using it

It uses the screens PaperGo already has:
- **Configure:** the built-in card in workspace workflow settings, where managers turn it off, set parameters, or copy it to customise.
- **Review:** the task inbox.
- **Undo:** the item's revision history and restore.
- **File an existing document:** a manual run on it.

## Phases

0. **Measure.** Run it on about 200 real, anonymised documents in English, Spanish and Catalan to set the confidence presets. Below 90% precision at `cautious`, ship with suggestions only (apply threshold 1.0).
1. **Build.** The excerpt option, the role and built-in, and docs:
   - `docs/decisions.md`: the `max_tokens` option;
   - `docs/workflows.md`: a row in the built-in table;
   - the README requirement table.

   Tests use a fake decisions engine and run on SQLite and PostgreSQL. They cover:
   - only empty fields are filled;
   - the run descends levels and stops when unsure;
   - one guarded write, and a changed item is skipped;
   - one task holds the uncertain choices;
   - a newer file replaces the run;
   - a new library gets the provider by default;
   - nothing is registered without the engine;
   - a workspace export and import keeps the parameters.
2. **Later, only if usage asks for it.** An "Auto" marker with undo in the item inspector, a dedicated settings tab, the document shown beside the review form, list items, and filing existing documents in bulk.

## Risks

| Risk | Mitigation |
| --- | --- |
| Wrong values | Cautious default, empty fields only, approval when unsure, undo through history |
| About 1.4 GB of model memory and an 11 s cold start | Unloaded after 5 idle minutes; managers turn it off per library |
| Term sets without descriptions classify by names only | Document that descriptions improve filing; the Phase 0 measurements show by how much |

## Decisions for the owner

1. Confirm the defaults:
   - on by default where the model exists;
   - `cautious`;
   - reviewers `["actor"]`;
   - the best single term for fields with several values;
   - keywords not filled.
2. A source of real documents for Phase 0.
