# Archiving

Archive documents and list items when they should leave everyday browsing. Archiving keeps the same item ID, folder, collection, properties, permissions, file versions, links, and content history. Archived items remain editable and downloadable. Folders, lists, libraries, and workspaces cannot be archived.

The main **Archive** page spans every workspace the caller can read, including Home. It provides immediate database-backed browsing and name search, with workspace, list/library, content type, keyword, archive date, and archived-by filters. Its default order is most recently archived first. Date-picker bounds use UTC. When search is configured, **Full text** searches the archive with the existing file-content search service and its indexing consistency.

Every list and library provisions a permanent **Archived** table view, including items in subfolders. It has a stable `system_key: "archived"`, independent of its generated ID. It cannot be renamed, edited, deleted, or made the opening default. Other saved views and smart folders can explicitly select Active, Archived, or All items. Normal search defaults to Active and offers the same three choices.

## State and permissions

`archived_at` and `archived_by` are resource lifecycle metadata; `archived` is derived from them. Archive/unarchive requires `item.write` scope and the item's existing write permission. Readers retain access to archived items they could otherwise read, including the usual head/published restrictions. Archive status is shared by both surfaces and applies without publishing.

A transition increments the resource lock version and atomically records audit and `item.archived` or `item.unarchived` events. It does not change content modification metadata, allocate a content revision, change publication pointers, or modify file bytes. Repeating a transition with the current version leaves state, version, and events unchanged; a stale version still fails.

Editing, publishing, and moving an item preserve archive status. Deletion moves it to the existing recycle bin and removes it from the archive. Recycle-bin restoration preserves archive status. Copies are new active items. Archived files continue reserving their sibling name and business keys.

WebDAV directory listings hide archived files. Direct paths still resolve for reads, downloads, and authorized writes; uploading to an archived file's path updates that same item without unarchiving it. Relationships and existing lookup references remain usable. Lookup discovery defaults to active items.

## API

| Method | Route | Behavior |
| --- | --- | --- |
| POST | `/v1/items/{id}/archive` | Archive an item; current `If-Match` required |
| POST | `/v1/items/{id}/unarchive` | Unarchive an item; current `If-Match` required |
| GET | `/v1/archive` | Browse readable archived items across workspaces |

Transitions return the resource with current allowed actions and its ETag. Missing `If-Match` returns 428; a stale ETag returns 412. Targeting a non-item returns 422. Collection bulk requests accept `archive` and `unarchive` with each item's ID and version, retaining the 100-operation limit and atomic rollback. A selection across collections uses independent collection transactions and reports partial failures.

`GET /v1/archive` accepts `q` (name search), `workspace_id`, `collection_id`, `content_type_id`, `keyword`, `archived_by`, `archived_from`, `archived_to`, `$top`, and `$skiptoken`. Time bounds are inclusive RFC3339 timestamps. Continuations bind the caller and filters; ordering uses archive time and ID. Each entry includes `can_write` for archive actions.

Browsing and search accept `archive=active|archived|all`; omission means `active`. Collection query specifications, saved views, and smart-folder definitions carry the same `archive` property. `$archived_at` and `$archived_by` are system fields for query filters, sorting, and columns. Archive predicates apply before paging, totals, grouping, facets, and search candidate limits. Saved queries grant no additional access.

The database is authoritative. Name browsing reflects committed transitions immediately. Both search stores index archive state through `search.index`, without rereading or embedding file bytes. Indexed searches may lag transitions, like other metadata edits; current database checks reject stale hits in the wrong archive selection and omit unverifiable facets. The archive remains browsable when search is disabled or rebuilding.

Content packages, backups, and delta synchronization preserve archive state. Configuration packages provision collection views without importing content. No automatic age, retention, or deletion policy is applied. Workflow scripts use the same archive APIs and can react to the archive events.
