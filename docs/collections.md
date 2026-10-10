# Collections

Lists and libraries are collections. Workspace owners create them with `POST /v1/resources/{workspace_id}/children`. Collection managers update or delete them at `/v1/resources/{id}` using the current ETag in `If-Match`.

Collection names are trimmed and limited to 200 UTF-16 code units. Descriptions are limited to 2000. Both creation and PATCH accept `description` and `allow_folders`. Folder creation is enabled by default. Disabling it prevents new folders at every depth; existing folders and their contents remain usable.

Deleting a collection removes its live content, indexes and relationships, including descendants with exclusive ACLs. Revisions, publications and audit records remain retained history. Workflows bound to the collection or with a trigger targeting it are disabled and deleted. Tombstones are hidden by ordinary reads.

Item PATCH merges supplied custom-value keys into the current item. Omitted keys retain their values; null removes an optional value. Defaults apply at creation. The resulting item must satisfy its current content type. REST, bulk updates, Smart Folder actions and workflow activities use this same mutation path.
