# Recycle bin

Deleting items or folders removes their live projections and hides their relationships, retaining content, permissions, versions, file references and links for recovery. Folder deletion requires `recursive=true` when it contains live children. Collections and workspaces delete their live trees.

`GET /v1/resources/{collectionID}/recycle-bin` lists deleted items and folders in a live collection, ordered by ID, with `$skiptoken` and `$top` pagination. Each entry includes `deleted_at` and `deleted_by`. Listing requires collection read access and only exposes entries for which the caller has write access, including module scope checks.

`POST /v1/resources/{collectionID}/recycle-bin/{itemID}/restore` restores an entry to its original folder if active, otherwise the collection root. It retains its ACL, metadata, file content, relationships and history, restores head/published projections and indexes, and increments its lock version. Links become visible when both endpoints are active and readable. The destination also requires write access. `recursive=true` restores folder descendants deleted at the same time, excluding entries deleted earlier. Name and unique-value conflicts roll back the whole request.

`DELETE /v1/resources/{collectionID}/recycle-bin/{itemID}` permanently removes a deleted entry, its revisions, publications, file references and permissions. Manage access to every affected entry and `list.write` are required. A folder with deleted children requires `recursive=true`. Audit events and workflow run records retain resource IDs. Storage objects enter a durable cleanup queue; the API attempts cleanup immediately and a background worker retries every minute after failures or restart. Ordinary APIs cannot read purged content, and restore returns not found.

Restore and purge do not require `If-Match`. Deleted collections and workspaces are outside the collection recycle-bin API. Restore and purge emit `item.restored` and `item.purged`; queued content events for permanently purged items are skipped. Purge events have no live item for workflow actions.
