# Web frontend backlog

The web UI in `web/` talks to the current `/v1` API. This list tracks screens left out because PaperGo has no API for them, screens still to build where the API exists, and places the UI follows the API in a narrower way than the contract allows.

## Not in the product

These areas have no PaperGo API, so the UI does not include them.

- Password, passkey and OAuth sign-in, and a server end-session. Bearer deployments store a token for the browser session. Proxy deployments use the browser’s HTTP credentials; the frontend loads the verified profile without a token form.
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

- The File tab restores bytes using a known file version number. There is no file-version list endpoint; metadata history entries contain `blob_id` without a file version number.
- The versions tab restores metadata with `POST /v1/items/{id}/revisions/{number}/restore`. File bytes stay on the current version.
- Move offers the lists or libraries of the same kind in the item's workspace and browses their folders. Each folder level shows folders among its first 200 children.
- The collection shows one level of children at a time.
- Preference language and number-format choices include tags the server may reject. The server accepts a BCP-47 tag whose base language matches with exact confidence. A rejected value returns 422.
- Plain browsing reads `GET /v1/resources/{id}/children` of the open folder or collection. A saved view or a column sort uses `POST /v1/resources/{id}/query` with the view's query, the open folder as `parent_id`, the search box as `search` and the chosen sort; the search box replaces a view's own search text. Search matches whole words.
- Smart folder create stores a name, an owner (personal or one workspace), folder inclusion and one comparison with a text, number or boolean value. The group path stays in page state.
- Inbox counts on Home come from `POST /v1/resources/{id}/query` with `include_total`. That total can include folders.
- Without a saved view, a table shows the name, the first four columns of the default content type and the modified date. Column headers sort by the name, dates, people and filterable (indexed, ready) single-value columns; the sort is the `sort` URL parameter and replaces the view's sort. Folders sort among items.
- Table cells and cards name people from `GET /v1/identity/users` (when the caller can read users), terms and keywords from `GET /v1/terms?ids=` (when the caller has `taxonomy.read`) and looked-up items through `$batch` reads, 20 per request. Unreadable looked-up items read "Unavailable item".
- `GET /v1/people` returns subject strings. Display names come from `GET /v1/identity/users` when the caller has `user.read`.

## What the UI matches

- Actions follow `access` on `GET /v1/resources/{id}`: adding, editing, moving and deleting need `write`, publishing `publish`, settings and permissions `manage`. Selection and bulk actions use the list's access; an item with its own stricter permissions can still refuse them. Creating workspaces needs the `workspace.create` scope.
- Resources are `/v1` and JSON fields are snake_case. Bearer deployments send `Authorization: Bearer`; proxy deployments use same-origin browser HTTP credentials. `/auth/config` selects the supported mode before the app mounts.
- Live updates use `fetch` on `GET /v1/events`. The stream uses the same bearer or proxy credentials as other requests.
- A personal home workspace is `personal_owner_id`. The sidebar hides it. Home and Inbox still open it through `GET /v1/identity/me/home` and `GET /v1/identity/me/inboxes`. Search includes it because search requires `workspace_id`.
- Preference writes send `If-Match`, including `"0"` when the user has no saved row. A stale preference write is 409.
- One organization per deployment.
- The API rejects a new relationship whose items are in different workspaces.
