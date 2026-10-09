# Smart folders

Smart folders save live queries across lists and libraries. They store definitions, never copies of items. Personal definitions are private to their owner and can span all readable workspaces. Shared definitions belong to one workspace: readers can query them, managers can create, replace or delete them. PUT and DELETE require the definition's current numeric ETag. Ownership and workspace are immutable.

Create a definition with `POST /v1/smart-folders`, browse with GET, and read or replace it at `/v1/smart-folders/{id}`. Optional `workspace_id` narrows browsing. Personal definitions do not appear in workspace audit feeds. Shared definition mutations are audited.

Each workspace supports up to 100 shared definitions. Their mutations also advance the workspace ETag. Workspace managers can GET `/v1/workspaces/{id}/smart-folders/export` and POST `/v1/workspaces/{id}/smart-folders/import` with a current workspace `If-Match`. Both responses carry the workspace ETag. Packages contain `folders` with names, descriptions and definitions, excluding personal folders and ownership IDs. Collection names and template/content-type keys stay portable; selected terms become `term_set_key` plus a root-to-leaf `path` of term names. The target taxonomy must exist. Filter literals remain literal.

Import atomically merges shared definitions by exact name, creates missing definitions, replaces changed definitions and preserves IDs. Reapplying an identical package creates no revisions, audit events or version changes. An invalid term path, duplicate name, invalid definition, quota violation or stale ETag rolls back the whole package. Import reports `created`, `updated`, `workspace_version` and the affected definitions. It leaves other definitions intact.

```json
{
  "name": "Open invoices",
  "workspace_id": "WORKSPACE_UUID",
  "personal": false,
  "definition": {
    "collections": ["Invoices"],
    "content_types": ["invoice"],
    "filter": {"field": "status", "op": "eq", "value": "open"},
    "group_by": [{"field": "issued", "by": "year"}, {"field": "customer"}]
  }
}
```

`collections` matches collection names; `templates` matches adopted template keys; `content_types` matches type keys or names. Selectors ignore case, OR within each selector, and AND between selectors. Omitted selectors include all readable collections. The query rejects more than 100 selected collections rather than returning incomplete results. Catalogs are fetched in batches and membership, counts and pagination run in SQL against indexed values. Equivalent collection predicates share one SQL branch; distinct schema/predicate shapes use UNION ALL. Queries are bounded to 30,000 SQL parameters.

`terms` contains at most 20 taxonomy IDs. Membership includes descendants, across any indexed term fields in the collection. `term_match` is `all` by default or `any`. A scoped definition's terms must belong to its workspace. The caller needs access to their term sets. Filters use the same bounded typed AST as collection views: 32 nodes, depth six, indexed fields. Collections with incompatible filters are skipped; if none can compile, the query returns a validation error.

POST `/v1/smart-folders/{id}/query` with `surface`, `path`, `after` and `limit`. Responses contain collection/workspace identity, items and a cursor; `include_total` adds the authorized total. `auto` chooses head for draft readers and published content for readers. Results sort by selected-surface modification time descending with ID ascending ties. Cursors bind the definition version, caller, surface, path, schema snapshots and resolved filter values.

POST `/v1/smart-folders/{id}/query/groups` for the next metadata navigation level. Up to three scalar indexed fields can form a path. Date/datetime fields support `by: "year"` or `"month"`. Cross-collection grouping requires compatible types, decimal scales and reference scopes. Pass group values as strings in `path`; pass JSON null for `(empty)`. Term labels require taxonomy access. The endpoint returns an empty group list at the final level.

`include_folders` includes physical folders alongside items. Physical folders have system metadata and tags but no custom values or content type; term/content-type selectors exclude them. Custom `missing` filters and empty navigation groups can include them. Smart folder navigation does not move physical resources.

Classify an item with POST `/v1/smart-folders/{id}/items`. Supply `folder_version`, `collection_id`, optional navigation `path`, and either `item_id` with its current `version`, or `create` with the ordinary bulk-create item fields. New items may specify `parent_id`; the default parent is the collection. Existing items retain their parent. The response is the item with its new ETag: 201 for creation, 200 for edits. Read access to a definition does not grant write access to content.

Classification infers positive equality predicates joined through AND, resolves relative references, adds selected terms to a compatible content-type field, and sets ordinary navigation fields. OR/NOT and range predicates require suitable supplied or existing values. Computed year/month paths require a date that already fits. System timestamps and actors come from the normal item pipeline. Contradictory scalar assignments are rejected. The entire write rolls back unless the resulting head matches the definition and path.

DELETE `/v1/smart-folders/{id}/items/{itemID}` unclassifies an existing member; it does not delete or move it. Send `If-Match` for the item and a JSON body with `folder_version`. The action clears matching equality values/tags and selected terms or descendants while retaining unrelated values and the item name. Required fields, business keys, cross-field rules, immutable revisions and publishing operate through the ordinary write pipeline. If the result still matches, unclassification returns a validation error and rolls back. Explicitly published collections retain the published classification until an editor publishes the new head.

Filters in smart folders, ordinary queries and saved views accept `value_ref` instead of `value`: `me`, `today`, `yesterday`, `tomorrow`, `weekStart`, `weekEnd`, `monthStart`, `monthEnd`, `last7Days`, `next7Days`, `last30Days`, `next30Days`. Date references resolve in UTC; weeks run Monday–Sunday. Datetime references resolve to midnight. Plain `value` strings stay literal. Date references require date/datetime fields.

The implementation follows the behavior reviewed in PaperDotNet's [smart folder definitions](https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Lists/PaperDotNet.Lists/Features/SmartFolders.cs), [query engine](https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Lists/PaperDotNet.Lists/Features/SmartFolderQuery.cs) and [templates](https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Lists/PaperDotNet.Lists/Features/SmartFolderTemplates.cs). PaperGo uses its typed query AST and separate physical folders and content surfaces.

`BenchmarkSmartFolderQuery100Collections` creates 100 collections with ten items each, filters an indexed status field, and reads 50 results plus an authorized count of 500. Run `go test ./internal/dms -run '^$' -bench '^BenchmarkSmartFolderQuery100Collections$' -benchtime=3x`. This is a reproducible workload for assessing query changes; timings depend on hardware and database size.
