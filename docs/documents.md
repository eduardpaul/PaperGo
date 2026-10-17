# Documents

`POST /v1/resources/{libraryID}/documents` creates an item, metadata and file in one request. Send `multipart/form-data` with one `file` part and an optional `metadata` JSON form field. Either part can come first. Metadata supports `name` (defaults to the filename), `content_type_id` (an attached type), `folder_id` (a folder in that library), `keywords`, `values` and `languages`. Custom values receive ordinary schema validation and creation defaults. The destination library and folder require write access and `list.write` and `document.write`.

Example metadata: `{"name":"Invoice","folder_id":"<folder UUID>","values":{"amount":"12.50"},"languages":"eng+deu"}`. Language hints accept up to six lowercase codes joined with `+` and are stored on the immutable file version. Replacement uploads can send `X-OCR-Languages`; restoration clears the hint. Document extraction is a separate processing capability.

The file streams under the configured upload limit; metadata is limited to 64 KiB and multipart overhead to an additional 128 KiB. Successful creation returns `201` with `item`, `file`, `duplicates`, `Location` and the new item ETag. Its first content revision already contains both metadata and file. Malformed requests, invalid metadata, duplicate blocking and failed writes leave no item and remove the stored object. The library's normal publishing setting controls whether that first revision is published or a draft.

A library item holds metadata and immutable file versions. `PUT /v1/items/{id}/content` streams a new file using `X-Filename`, `Content-Type` and the current numeric `If-Match`. The response includes the file version number and the new resource ETag. File version numbers, metadata revision numbers and resource lock versions are independent.

Lists and libraries, including Documents templates and personal Home libraries,
default to versioning off. With versioning off, replacing a file queues its
previous bytes for reclamation after a one-hour grace period. Current published
files, staged uses and workflow leases defer reclamation until released. File
metadata remains in history with its availability, and bytes shared by other
files remain stored. Enable versioning in collection settings to keep replacement
files and record metadata history; retention accepts 1–500 metadata entries.

Libraries accept `duplicate_policy: "allow" | "warn" | "block"` on creation and PATCH. It defaults to `warn`; changing it requires library manage access and the current `If-Match`. Collection templates can set the policy. Uploads compare SHA-256 hashes against current files visible to the caller across libraries and workspaces, excluding the item being replaced, historical files and deleted items. `warn` returns up to 20 matching documents in `duplicates`; `allow` returns an empty list; `block` rejects matching uploads with `409 duplicate_file`. File creation and replacement, including WebDAV, enforce blocking inside the same transaction as their revisions. Failed REST uploads remove their stored object. File restoration follows the version history and does not apply upload duplicate policy.

`GET /v1/items/{id}/content/versions` lists the retained files of an item, newest first, with `$top` and `$skiptoken` paging. Each entry has its `version` number, `filename`, `content_type`, `size`, `sha256`, `created_at`, `created_by` (who uploaded or restored it), `is_current` (the file of the caller's visible revision) and `availability` (`available`, `pending` or `reclaimed`). Draft readers see every version; ordinary readers see only the published file. It requires `document.read`. A workflow asks for the bytes back with [`file.reclaim`](workflows.md#reclaiming-a-file-version). The version stays in history. `pending` still downloads. After the one-hour grace period an unprotected version becomes `reclaimed`: its download and restore answer `410 content_unavailable`, and file history shows it as unavailable. Bytes another file still needs stay stored.

`GET /v1/items/{id}/text` returns the text read from the file the caller may read (the current one, or `blob_id`), once a workflow with [`document.read_text`](workflows.md#reading-text) read it: `pages` (`page`, `text`, and `ocr` for recognized pages), `page_count`, `chars`, `language`, `ocr_pages`, `skipped_pages`, `truncated`, `text_id` and `read_at`. Before that it answers `404 text_not_read`. It requires `document.read` and follows the same visibility as downloads.

`POST /v1/items/{id}/content/versions/{number}/restore` restores an earlier file that still has its bytes as a new file version. A reclaimed version answers `410 content_unavailable`. It requires item write permission, `list.write` and `document.write` and the current resource `If-Match`. It retains current metadata, restores the selected bytes and filename, and follows the library's publication settings. In a library with explicit publishing, restoring creates a draft and leaves the publication unchanged. Earlier files and metadata revisions remain immutable. Identical SHA-256 and size reuse one stored byte object across items and workspaces. File names, media types, language hints and access remain specific to each file version. Purging an item queues its objects once; cleanup rechecks all file-version references while holding the database write lock. Bytes shared by another item remain available. A periodic collector runs each minute and removes unreferenced completed objects older than one hour, including objects left by interrupted uploads. Failed storage deletion stays queued for retry.

## Renaming and upload name conflicts

Use an item's **Rename** action or edit its name in Details. The new content
revision freezes the attachment display name used by the File tab, downloads,
WebDAV and workflow file reads. Omitting an extension keeps the current one.
Published readers keep the published filename until the rename is published;
older revisions and uploaded file-version metadata retain their names. Renaming
does not replace bytes or raise a file-change event. Sibling names remain unique
ignoring case, and conflicts leave the item unchanged.

An upload name collision returns `409 name_conflict` with `existing_item` (ID and
resource version) only when the uploader can write that item. In the upload tray,
choose **Rename** to retry with a new file and resource name, or **Replace
existing** to upload to that item with the returned `If-Match` version. A concurrent
edit rejects the replacement. Inaccessible items and folders disclose no replace
target. Rejected uploads remove their temporary stored objects.

## Personal and group Inbox intake

GET `/v1/identity/me/home` provisions one Home workspace with Documents and Inbox libraries. Both use the Documents template, shared Document content type, versioning off and the default document view. Their IDs remain stable even when names change.

POST `/v1/identity/me/inbox/documents` uploads to personal Inbox. GET `/v1/identity/me/inboxes` returns personal Inbox first, then readable bindings for effective group memberships, ordered by group name. Discovery can include a read-only destination; group membership does not grant library access.

GET/PUT/DELETE `/v1/identity/groups/{id}/inbox` reads, sets or removes a binding. PUT accepts `{ "library_id": "..." }`. Changes require `document.write` and Manage access to both the new and existing live library; no group administration scope or binding ETag is required. A deleted destination can be replaced or unbound. Removing a binding or group preserves library contents.

POST `/v1/identity/groups/{id}/inbox/documents` requires effective group membership. Both Inbox upload routes use streamed multipart `file` and optional JSON `metadata`, accepting name, keywords, values and languages. They reject folder and content-type overrides. Existing upload scopes (`document.write`, `list.read`, `list.write`) and destination write access apply, with membership, binding and permissions checked again at commit. A rebinding during streaming returns 409; failed uploads leave no item or attached file.
