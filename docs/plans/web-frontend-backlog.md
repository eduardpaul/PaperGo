# Web frontend backlog

The web UI in `web/` talks to the current `/v1` API. This list tracks screens left out because PaperGo has no API for them, screens still to build where the API exists, and places the UI follows the API in a narrower way than the contract allows.

## Not in the product

These areas have no PaperGo API, so the UI does not include them.

- Password, passkey and OAuth sign-in, and a server end-session. Sign-in stores a bearer token for the browser session.
- Tasks.
- Calendar, calendar feeds and `.ics`.
- Approvals.
- Notifications, delivery channels, webhooks, quiet hours and digests.
- Follow and subscriptions.
- Comments, mentions and an item activity tab. The audit feed is organization-wide.
- Note pages and `[[title]]` links.
- Document page tools: thumbnails, OCR rerun, rotate, reorder, split, page images and processing status.
- Search modes other than `q`, including semantic, exact and meaning search, facets, snippets and page hits.

## API exists, screen does not

All product screens in this section are implemented against the registered `/v1` routes.

- Administration: organization profile and defaults, users and account status, nested groups and inbox bindings, roles and assignments.
- Term store: groups, sets, hierarchy, moves, merges, deprecation, CSV imports, keyword promotion and taxonomy packages.
- Audit filters and organization portability packages with operation progress.
- Collections: saved view/query editing, board/calendar/gallery layouts, shared field editing/removal and content type attachments.
- Content type and relationship type catalogs; typed item links with attribute forms.
- Workflows: visual activity/branch editing, versions, built-in copies and schema-driven manual launches.
- Items: file-byte restoration, publication history and effective schema inspection.
- Selected-item bulk field updates and collection operation progress; workspace tag browsing.
- Smart folder classification/unclassification and shared folder packages in workspace settings.

## Client protocol

- Delta sync (`GET /v1/resources/{id}/delta`) is available for clients. PaperDotNet has no screen to port for it. The web frontend refreshes queries through the live event stream.
- `$batch` runs selected resource deletions in groups of up to 20 and reports failures per request.

## Narrower than the contract

- The Content tab restores bytes using a known file version number. There is no file-version list endpoint; metadata history entries contain `blob_id` without a file version number.
- The versions tab restores metadata with `POST /v1/items/{id}/revisions/{number}/restore`. File bytes stay on the current version.
- Move accepts any collection id. The picker lists collections in the item's current workspace, and a folder id can be pasted.
- The collection shows one level of children at a time.
- Term and keyword chips show the id until that id appears in the current search results.
- Preference language and number-format choices include tags the server may reject. The server accepts a BCP-47 tag whose base language matches with exact confidence. A rejected value returns 422.
- A non-empty collection search box lists children of the open folder or collection. Saved view query has no extra search term, so search and the saved view are not combined.
- Smart folder create stores a name, an owner (personal or one workspace), folder inclusion and one comparison with a text, number or boolean value. The group path stays in page state.
- Inbox counts on Home come from `POST /v1/resources/{id}/query` with `include_total`. That total can include folders.
- `GET /v1/people` returns subject strings. Display names come from `GET /v1/identity/users` when the caller has `user.read`.

## What the UI matches

- Resources are `/v1` and JSON fields are snake_case. The browser sends `Authorization: Bearer`.
- Live updates use `fetch` on `GET /v1/events`. The stream requires the bearer token, so it is not an `EventSource`.
- A personal home workspace is `personal_owner_id`. The sidebar hides it. Home and Inbox still open it through `GET /v1/identity/me/home` and `GET /v1/identity/me/inboxes`. Search includes it because search requires `workspace_id`.
- Preference writes send `If-Match`, including `"0"` when the user has no saved row. A stale preference write is 409.
- One organization per deployment.
- The API rejects a new relationship whose items are in different workspaces.
