# Smart folders

Smart folders save live queries across lists and libraries. They store definitions, never copies of items. Personal definitions are private to their owner and can span all readable workspaces. Shared definitions belong to one workspace: readers can query them, managers can create, replace or delete them. PUT and DELETE require the definition's current numeric ETag. Ownership and workspace are immutable.

Create a definition with `POST /v1/smart-folders`, browse with GET, and read or replace it at `/v1/smart-folders/{id}`. Optional `workspace_id` narrows browsing. Personal definitions do not appear in workspace audit feeds. Shared definition mutations are audited.

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

`collections` matches collection names; `templates` matches adopted template keys; `content_types` matches type keys or names. Selectors ignore case, OR within each selector, and AND between selectors. Omitted selectors include all readable collections. The query rejects more than 100 selected collections rather than returning incomplete results. Catalogs are fetched in batches and membership, counts and pagination run in SQL against indexed values.

`terms` contains at most 20 taxonomy IDs. Membership includes descendants, across any indexed term fields in the collection. `term_match` is `all` by default or `any`. A scoped definition's terms must belong to its workspace. The caller needs access to their term sets. Filters use the same bounded typed AST as collection views: 32 nodes, depth six, indexed fields. Collections with incompatible filters are skipped; if none can compile, the query returns a validation error.

POST `/v1/smart-folders/{id}/query` with `surface`, `path`, `after` and `limit`. Responses contain collection/workspace identity, items, an authorized total, and a cursor. `auto` chooses head for draft readers and published content for readers. Results sort by selected-surface modification time descending with ID ascending ties. Cursors bind the definition version, caller, surface, path, schema snapshots and resolved filter values.

POST `/v1/smart-folders/{id}/query/groups` for the next metadata navigation level. Up to three scalar indexed fields can form a path. Date/datetime fields support `by: "year"` or `"month"`. Cross-collection grouping requires compatible types, decimal scales and reference scopes. Pass group values as strings in `path`; pass JSON null for `(empty)`. Term labels require taxonomy access. The endpoint returns an empty group list at the final level.

`include_folders` includes physical folders alongside items. Physical folders have system metadata and tags but no custom values or content type; term/content-type selectors exclude them. Custom `missing` filters and empty navigation groups can include them. Smart folder navigation does not move physical resources.

Filters in smart folders, ordinary queries and saved views accept `value_ref` instead of `value`: `me`, `today`, `yesterday`, `tomorrow`, `weekStart`, `weekEnd`, `monthStart`, `monthEnd`, `last7Days`, `next7Days`, `last30Days`, `next30Days`. Date references resolve in UTC; weeks run Monday–Sunday. Datetime references resolve to midnight. Plain `value` strings stay literal. Date references require date/datetime fields.

The implementation follows the behavior reviewed in PaperDotNet's [smart folder definitions](https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Lists/PaperDotNet.Lists/Features/SmartFolders.cs), [query engine](https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Lists/PaperDotNet.Lists/Features/SmartFolderQuery.cs) and [templates](https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Lists/PaperDotNet.Lists/Features/SmartFolderTemplates.cs). PaperGo uses its typed query AST and separate physical folders and content surfaces.
