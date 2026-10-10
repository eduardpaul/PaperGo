# Collections

Lists and libraries are collections. Workspace owners create them with `POST /v1/resources/{workspace_id}/children`. Collection managers update or delete them at `/v1/resources/{id}` using the current ETag in `If-Match`.

Collection names are trimmed and limited to 200 UTF-16 code units. Descriptions are limited to 2000. Both creation and PATCH accept `description` and `allow_folders`. Folder creation is enabled by default. Disabling it prevents new folders at every depth; existing folders and their contents remain usable.

Deleting a collection removes its live content and indexes and hides relationships, including descendants with exclusive ACLs. Revisions, publications and audit records remain retained history. Workflows bound to the collection or with a trigger targeting it are disabled and deleted. Tombstones are hidden by ordinary reads. Items and folders deleted inside live collections support [recycle-bin recovery and permanent purge](recycle-bin.md).

Item PATCH merges supplied custom-value keys into the current item. Omitted keys retain their values; null removes an optional value. Defaults apply at creation. The resulting item must satisfy its current content type. REST, bulk updates, Smart Folder actions and workflow activities use this same mutation path.

Item history is available at `GET /v1/items/{id}/revisions` and `GET /v1/items/{id}/revisions/{number}` with draft-read permission. Each result includes its frozen schema, whether it is the current head, and the fields changed from the preceding revision. `$name` and `$tags` identify system metadata. `after_revision` paginates by revision number.

`POST /v1/items/{id}/revisions/{number}/restore` requires write permission and the item's current `If-Match`. It restores historical metadata through current validation, clears current fields absent from the snapshot, and creates a new revision with ordinary indexing, publication and events. Removed fields remain removed. The current file blob is retained.
# Collection templates

GET `/v1/collection-templates` lists built-in Documents and Contacts templates and templates registered by modules. It requires `list.read`. POST `/v1/resources/{workspaceID}/children` with `name` and `template_key` creates a complete collection with settings, shared content types and saved views in one transaction. The caller needs workspace manage access, `list.read` and `contentType.read`. A template determines the collection kind, folder policy, publishing and WebDAV settings; `content_type_ids` cannot be supplied with it.

Template content types have a stable organization provisioning identity. Reusing a template attaches the same live type and preserves curator customizations. Every item retains immutable revision history. Modules call `RegisterCollectionTemplate` before serving requests. Workspace schema recipes remain available for applying fields and rules to an existing collection.
