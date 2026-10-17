# Smart folders

Smart folders save live queries across lists and libraries. Every definition has an explicit `mode`: `query` for a saved search or `classification` for a live folder that also permits classify/unclassify actions. They store definitions, never copies of items. Personal definitions are private to their owner and can span all readable workspaces. Shared definitions belong to one workspace: readers can query them, managers can create, replace or delete them. PUT and DELETE require the definition's current numeric ETag. Ownership and workspace are immutable.

Create a definition with `POST /v1/smart-folders`, browse with GET, and read or replace it at `/v1/smart-folders/{id}`. Optional `workspace_id` narrows browsing. Personal definitions do not appear in workspace audit feeds. Shared definition mutations are audited.

The web editor uses labelled fields, typed values and readable comparisons. Choose workspace scope, lists or libraries, collections, content types, keywords and custom indexed fields. New keyword labels in the keyword selector are added to the organization Keywords term set before previewing or saving the rule. File type rules use `$file_type`, the MIME type of the authorized visible file revision. Date rules offer UTC-relative values such as Past week and Past month. Incompatible field definitions are omitted until a collection resolves the scope. Ownership and workspace scope are fixed when a definition is saved.

POST `/v1/smart-folders/preview` with `workspace_id` (optional) and `definition` to evaluate unsaved rules. It requires `list.read`, returns up to five authorized items, and counts up to 10,000 matches. `total_capped: true` means the count is a lower bound. Preview neither saves definitions nor changes items, versions or audit events. The editor waits for a successful preview before saving.

Opening a smart folder displays matching items immediately in table or grid layout with collection badges, item opening, context menus and standard sorting. Group browsing is optional. Saved searches have no classify/unclassify actions; classification folders explain which values and keywords those actions apply. Sidebar menus open, edit or delete definitions according to current ownership, module scopes and workspace permissions.

Each workspace supports up to 100 shared definitions. Their mutations also advance the workspace ETag. Workspace managers can GET `/v1/workspaces/{id}/smart-folders/export` and POST `/v1/workspaces/{id}/smart-folders/import` with a current workspace `If-Match`. Both responses carry the workspace ETag. Packages contain `folders` with names, descriptions and definitions, excluding personal folders and ownership IDs. Collection names and template/content-type keys stay portable; selected terms become `term_set_key` plus a root-to-leaf `path` of term names. The target taxonomy must exist; a [taxonomy package](application-foundation.md#controlled-taxonomy) sets it up. Filter literals remain literal.

Import atomically merges shared definitions by exact name, creates missing definitions, replaces changed definitions and preserves IDs. Reapplying an identical package creates no revisions, audit events or version changes. An invalid term path, duplicate name, invalid definition, quota violation or stale ETag rolls back the whole package. Import reports `created`, `updated`, `workspace_version` and the affected definitions. It leaves other definitions intact.

```json
{
  "name": "Open invoices",
  "workspace_id": "WORKSPACE_UUID",
  "personal": false,
  "definition": {
    "mode": "query",
    "collection_kind": "list",
    "collections": ["Invoices"],
    "content_types": ["invoice"],
    "filter": {"field": "status", "op": "eq", "value": "open"},
    "group_by": [{"field": "issued", "by": "year"}, {"field": "customer"}]
  }
}
```

`collection_kind` optionally restricts results to `list` or `library`. `collections` matches collection names; `templates` matches adopted template keys; `content_types` matches type keys or names. Selectors ignore case, OR within each selector, and AND between selectors. Omitted selectors include all readable collections. The query rejects more than 100 selected collections rather than returning incomplete results. Catalogs are fetched in batches and membership, counts and pagination run in SQL against indexed values. Equivalent collection predicates share one SQL branch; distinct schema/predicate shapes use UNION ALL. Queries are bounded to 30,000 SQL parameters.

`terms` contains at most 20 taxonomy IDs. Membership includes descendants and terms merged into them, across the collection's indexed term fields of the term's set and, for keywords, its indexed keywords fields. A selected term that was merged stands for the term it was merged into. `term_match` is `all` by default or `any`. Definitions reference the organization term store, including when scoped to a workspace. Selecting and resolving taxonomy terms requires `taxonomy.read`; content queries continue to enforce resource ACLs. Filters use the same bounded typed AST as collection views: 32 nodes, depth six, indexed fields. Collections with incompatible filters are skipped; if none can compile, the query returns a validation error.

POST `/v1/smart-folders/{id}/query` with `surface`, optional `path`, `sort` and `include_total` in the JSON body. Set `$top` in the URL (default 50, clamped to 1–500). Responses contain `value` entries with collection/workspace identity and items; `include_total` adds the authorized total. Follow `@odata.nextLink` using the same POST body; the link carries `$skiptoken` and is null on the final page. `auto` chooses head for draft readers and published content for readers. An omitted path returns all matching items directly, including definitions with grouping. Each entry includes `collection_kind` and collection name for provenance and item navigation. Sorting defaults to selected-surface modification time descending; `sort: {"field":"$name","direction":"asc"}` sorts by name. `$created_at` and `$modified_at` also support either direction, with ID ties following that direction. Cursors bind the definition version, caller, surface, path, sort, schema snapshots and resolved filter values.

POST `/v1/smart-folders/{id}/query/groups` for the next metadata navigation level. Up to three scalar indexed fields can form a path. Date/datetime fields support `by: "year"` or `"month"`. Cross-collection grouping requires compatible types, decimal scales and reference scopes. Pass group values as strings in `path`; pass JSON null for `(empty)`. Term labels require taxonomy access. The endpoint returns an empty group list at the final level.

`include_folders` includes physical folders alongside items. Physical folders have an attached content type and optional custom values, terms and keywords. They participate in content-type selectors, taxonomy filters and metadata navigation. Their metadata is immediately visible on both surfaces. Smart folder navigation does not move physical resources.

Only definitions with `mode: "classification"` accept classification changes. Classify an item with POST `/v1/smart-folders/{id}/items`. Supply `folder_version`, `collection_id`, optional navigation `path`, and either `item_id` with its current `version`, or `create` with the ordinary bulk-create item fields. New items may specify `parent_id`; the default parent is the collection. Existing items retain their parent. The response is the item with its new ETag: 201 for creation, 200 for edits. Read access to a definition does not grant write access to content.

Classification infers positive equality predicates joined through AND, resolves relative references, adds selected terms to a compatible content-type field, and sets ordinary navigation fields. OR/NOT and range predicates require suitable supplied or existing values. Computed year/month paths require a date that already fits. System timestamps and actors come from the normal item pipeline. Contradictory scalar assignments are rejected. The entire write rolls back unless the resulting head matches the definition and path.

DELETE `/v1/smart-folders/{id}/items/{itemID}` unclassifies an existing member; it does not delete or move it. Send `If-Match` for the item and a JSON body with `folder_version`. The action clears matching equality values/keywords and selected terms or descendants while retaining unrelated values and the item name. Required fields, business keys, cross-field rules, immutable revisions and publishing operate through the ordinary write pipeline. If the result still matches, unclassification returns a validation error and rolls back. Explicitly published collections retain the published classification until an editor publishes the new head.

Filters in smart folders, ordinary queries and saved views accept `value_ref` instead of `value`: `me`, `today`, `yesterday`, `tomorrow`, `weekStart`, `weekEnd`, `monthStart`, `monthEnd`, `last7Days`, `next7Days`, `last30Days`, `next30Days`. Date references resolve in UTC; weeks run Monday–Sunday. Datetime references resolve to midnight. Plain `value` strings stay literal. Date references require date/datetime fields.

The implementation follows the behavior reviewed in PaperDotNet's [smart folder definitions](https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Lists/PaperDotNet.Lists/Features/SmartFolders.cs), [query engine](https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Lists/PaperDotNet.Lists/Features/SmartFolderQuery.cs) and [templates](https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Lists/PaperDotNet.Lists/Features/SmartFolderTemplates.cs). PaperGo uses its typed query AST and separate physical folders and content surfaces.

`BenchmarkSmartFolderQuery100Collections` creates 100 collections with ten items each, filters an indexed status field, and reads 50 results plus an authorized count of 500. Run `go test ./internal/dms -run '^$' -bench '^BenchmarkSmartFolderQuery100Collections$' -benchtime=3x`. This is a reproducible workload for assessing query changes; timings depend on hardware and database size.
