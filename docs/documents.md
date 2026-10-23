# Documents

`POST /v1/resources/{libraryID}/documents` creates an item, metadata and file in one request. Send `multipart/form-data` with one `file` part and an optional `metadata` JSON form field. Either part can come first. Metadata supports `name` (defaults to the filename), `content_type_id` (an attached type), `folder_id` (a folder in that library), `keywords`, `values` and `languages`. Custom values receive ordinary schema validation and creation defaults. The destination library and folder require write access and `item.write` and `document.write`.

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

`GET /v1/items/{id}/content/versions` lists the retained files of an item, newest first, with `$top` and `$skiptoken` paging. Each entry has its `version` number, `filename`, `content_type`, `size`, `sha256`, `created_at`, `created_by` (who uploaded or restored it), `is_current` (the file of the caller's visible revision) and `availability` (`available`, `pending` or `reclaimed`). Draft readers see every version; ordinary readers see only the published file. It requires `document.read`. `POST /v1/items/{id}/content/reclaim` asks for the bytes back ([reclaiming a file version](#reclaiming-a-file-version)). The version stays in history. `pending` still downloads. After the one-hour grace period an unprotected version becomes `reclaimed`: its download and restore answer `410 content_unavailable`, and file history shows it as unavailable. Bytes another file still needs stay stored.

`GET /v1/items/{id}/text` returns the text read from the file the caller may read (the current one, or `blob_id`), once [`POST /v1/items/{id}/text`](#reading-text) read it: `pages` (`page`, `text`, and `ocr` for recognized pages), `page_count`, `chars`, `language`, `ocr_pages`, `skipped_pages`, `truncated`, `text_id` and `read_at`. Before that it answers `404 text_not_read`. It requires `document.read` and follows the same visibility as downloads. `page`, `offset` and `max_chars` read part of the text: pages from `page`, starting `offset` characters (Unicode code points) into it, up to `max_chars` characters, with pages cut short marked `partial`; `next` gives the `page` and `offset` the following part starts at, and is null after the end. A continuation passes the first part's `text_id` too, and fails with `412 text_changed` when the text is another one now, as when the item got another file, so parts never mix two texts; the read then starts again from page 1. Without them the whole text is returned.

`POST /v1/items/{id}/content/versions/{number}/restore` restores an earlier file that still has its bytes as a new file version. A reclaimed version answers `410 content_unavailable`. It requires item write permission, `item.write` and `document.write` and the current resource `If-Match`. It retains current metadata, restores the selected bytes and filename, and follows the library's publication settings. In a library with explicit publishing, restoring creates a draft and leaves the publication unchanged. Earlier files and metadata revisions remain immutable. Identical SHA-256 and size reuse one stored byte object across items and workspaces. File names, media types, language hints and access remain specific to each file version. Purging an item queues its objects once; cleanup rechecks all file-version references while holding the database write lock. Bytes shared by another item remain available. A periodic collector runs each minute and removes unreferenced completed objects older than one hour, including objects left by interrupted uploads. Failed storage deletion stays queued for retry.

## Reading text

`POST /v1/items/{id}/text` reads the text of the item's current file as a background operation (`202 Accepted`), for API clients and workflow scripts alike: `ocr` (`auto`, `always`, `never`), `max_pages` (default 500, at most 2,000) and `languages` (OCR codes such as `eng+deu`; default the file's). It requires `document.write` and write access to the item.

- **PDFs:** each page's text layer is read with the linked PDFium SDK. With `ocr: auto`, a page without a text layer is rendered at 200 DPI and read by [OCR](ocr.md); `always` OCRs every page; `never` reads text layers only. PaperGo's own searchable PDFs (Photos to PDF) carry their OCR text, so they are not OCRed again.
- **Images** (JPEG, PNG, WebP, and the first page of a TIFF) are turned upright and read by OCR.
- **Without OCR** configured, `auto` reads text layers only and counts the pages it could not read in `skipped_pages`; `always` is refused.
- **Language:** the file's language hint (`languages` on upload, `X-OCR-Languages`) when it names one language, else the language detected among the hinted ones or `SEARCH_LANGUAGES`, else none.

The text is kept once per file bytes and reading settings (reader, OCR engine and models, mode, page bound, languages): an identical file, or the same file read again, reuses it without reading again (`reused`). The result has `text_id`, `item_id`, `blob_id`, `pages`, `chars`, `language`, `ocr_pages`, `skipped_pages`, `truncated` and `reused`; a file whose text cannot be read, such as a protected or broken PDF or another format, fails the operation. Later requests refer to the text by `text_id`. `GET /v1/items/{id}/text` returns it, content packages carry it with the file, and it is deleted when no file with its bytes remains.

## Artifacts and staged files

Requests that produce files, such as a smaller image or a PDF composed from photos, replace nothing themselves. Their operation's result is an **artifact**: a file made for an item from file versions, with the metrics reviewers need. It belongs to no workflow:

- Anyone who may write the item can ask, from a workflow script or not. An identical request (same item, file versions and options) reuses an unexpired artifact instead of computing it again; its result says `reused: true`.
- An artifact is kept for 7 days, and longer while a staged file holds it. `GET /v1/artifacts/{id}` and `/content` show it to readers of its item. Expired artifacts nothing holds are deleted hourly, and the storage collector frees their bytes after its grace period.

To review or keep an artifact, a consumer **stages** it with `POST /v1/artifacts/{id}/stage` (`201`, with the staged file's `id`). A staged file is that consumer's claim:

- Staged from a workflow script, it belongs to the run; otherwise to the person or client who staged it. Only the same consumer promotes or discards it: a run its own staged files, a caller without a run those staged without one, each with write access to the item. Staging needs write access, and every file the artifact was made from must still be current.
- `GET /v1/staged-files/{id}` and `/content` show it, with its artifact's file, to readers of its item.
- `POST /v1/staged-files/{id}/promote` makes it the item's current file. That is a new file version (`file.updated` with source `workflow` for replacements), with the item's metadata, revision history, audit and events like an upload. It succeeds only if every file it was made from is still its item's current file, so a candidate made from an old upload never replaces a newer one.
- With `recycle_sources: true` (default `false`), promotion also moves the other source items to the recycle bin in the same transaction. The caller must be able to write every source item. If any source changed or cannot be recycled, neither promotion nor recycling commits.
- `POST /v1/staged-files/{id}/discard` drops the claim; the artifact stays for reuse until it expires.

A completed or cancelled run discards its pending staged files. A failed run keeps them for a retry until the run is deleted. Deleting an item discards the staged files about it; purging it deletes its artifacts.

## Reclaiming a file version

`POST /v1/items/{id}/content/reclaim` with a `blob_id` asks for one file version's bytes back. The version keeps its id, number, filename, content type, size and checksum. The request marks it `pending`. A current file, a published file, a pending staged file's artifact (or a file it was made from), or a byte lease held by an operation defers the grace period (`deferred`, with `reason` `current`, `published`, `staged` or `in use`). Asking again for a version that is already pending does not move `reclaim_after`. After the one-hour object grace period, the collector marks an unprotected version `reclaimed` and deletes bytes that no other file still needs.

## Image optimization

`POST /v1/items/{id}/optimize-image` shrinks a page image without making its text unreadable. It is a background operation (`202 Accepted`) whose result is an [artifact](#artifacts-and-staged-files); it needs `document.write` and write access to the item, with the options below in its body and an optional `file_version` the request was made for. It is PaperDotNet's storage optimization, with the same parameters and results:

1. It keeps files that are not JPEG, PNG or WebP, and a `file_version` a newer file already replaced.
2. It decodes the image (at most 64 megapixels), applies its EXIF orientation, and keeps animated images unchanged.
3. A copy at most `max_analysis_dimension` pixels long (default 2600) goes to the OCR detector. Text lines that score at least `min_confidence` (default 50), are at least 6 analysis pixels high and at least 1.5 times as long as high count. Each line's height is its shortest side, projected back to the image's pixels.
4. The `percentile` (default 5) of those heights, interpolated and rounded, at least 4, is the smallest text. The scale is `target_height` (default 12) divided by it, between `minimum_scale` (default 0.05) and 1: images never grow.
5. The image is resized with the Mitchell filter (transparency flattened onto white) and encoded as WebP at `quality` (default 80).

Its result keeps the original, with a `reason`, when the image has no reliable text, is already WebP with its smallest text at the target, or the WebP file would not be smaller. Otherwise it makes the WebP file an [artifact](#artifacts-and-staged-files), named after the original, and its result has the `artifact_id` and `metrics` for reviewers: text regions, smallest text height, scale, sizes before and after, and the settings, detector and model hashes. Without an OCR engine (see [OCR configuration](ocr.md#configuration)) the request is refused. The [Optimize image storage](workflows.md#built-in-workflows) built-in stages the copy and asks for a review before it promotes it.

## Photos to PDF

`POST /v1/items/{id}/compose-pdf` ports PaperDotNet's photo to document conversion. It is a background operation on the primary item whose result is an [artifact](#artifacts-and-staged-files), with `item_ids` (the photos in page order, the item among them), `target_height` and `quality`:

1. **Snapshot.** Every item must be an item of the primary item's library that the caller may write, with a JPEG, PNG or WebP file. The request records each file version; at most 100 photos.
2. **Pages.** Each photo, in order, is decoded (at most 64 megapixels, not animated), turned upright and flattened onto white. Its text lines are measured as in [image optimization](#image-optimization), and the page is scaled so that its smallest line is `target_height` pixels high (default 32). Unlike PaperDotNet, pages never grow; a page without text keeps its size.
3. **Text.** OCR reads the full-resolution photo. Each line is placed, invisible, at its position on the scaled page, so the PDF can be searched and its text selected and copied. The text layer embeds a placeholder font and maps characters to Unicode, so it needs no system fonts and keeps every script.
4. **PDF.** Each page image is embedded at 150 pixels per inch, as JPEG at `quality` (default 80), or losslessly when that is smaller. The PDF is an artifact for the primary item, named after its file, with the other items as `others`.

The operation fails if a photo changes before the PDF is made; staging and promotion fail if one changes before they commit. Either way, start a new conversion. The [Photos to PDF](workflows.md#built-in-workflows) built-in promotes with `recycle_sources: true`, so the PDF replacement and recycling commit together and no upload can arrive between them.

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

Every file’s **Move** action lets you choose a workspace, library and folder, including destinations outside its current workspace. Moving requires write access to the file and the destination folder (or library root), and the destination library must support the file’s content type and metadata. Workspace write access is not required when the destination grants write access separately. The picker follows all workspace, library and folder pages and enables **Move here** after confirming destination write access. Moving from item details opens the file in its destination. **My documents** on Home opens the personal Documents library.

GET/PUT/DELETE `/v1/identity/groups/{id}/inbox` reads, sets or removes a binding. PUT accepts `{ "library_id": "..." }`. Changes require `document.write` and Manage access to both the new and existing live library; no group administration scope or binding ETag is required. A deleted destination can be replaced or unbound. Removing a binding or group preserves library contents.

POST `/v1/identity/groups/{id}/inbox/documents` requires effective group membership. Both Inbox upload routes use streamed multipart `file` and optional JSON `metadata`, accepting name, keywords, values and languages. They reject folder and content-type overrides. Existing upload scopes (`document.write`, `item.read`, `item.write`) and destination write access apply, with membership, binding and permissions checked again at commit. A rebinding during streaming returns 409; failed uploads leave no item or attached file.
