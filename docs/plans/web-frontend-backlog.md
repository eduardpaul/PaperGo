# Web frontend backlog

The web UI in `web/` talks to the current `/v1` API. This list tracks screens left out because PaperGo has no API for them, screens still to build where the API exists, and places the UI follows the API in a narrower way than the contract allows.

## Not in the product

These areas have no PaperGo API, so the UI does not include them.

- Password and passkey sign-in in PaperGo itself, and a server end-session. OIDC deployments with `OIDC_CLIENT_ID` sign in at the identity provider (authorization code with PKCE), renew access with refresh tokens and sign out there; passwords, passkeys and MFA belong to the provider. Other bearer deployments store a pasted token for the browser session. Proxy deployments use the browser’s HTTP credentials; the frontend loads the verified profile without a token form.
- Tasks.
- Calendar, calendar feeds and `.ics`.
- Approvals.
- Notifications, delivery channels, webhooks, quiet hours and digests.
- Follow and subscriptions.
- Comments, mentions and an item activity tab. The audit feed is organization-wide.
- Note pages and `[[title]]` links.
- Document page tools: thumbnails, OCR rerun, rotate, reorder, split, page images and processing status.
- Search modes other than keyword (semantic, hybrid), page hits, and field filters (`POST /v1/search` with `filter`, `GET /v1/search/fields`) on the search page.

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

- The versions tab restores metadata with `POST /v1/items/{id}/revisions/{number}/restore`. File bytes stay on the current version.
- Move offers the lists or libraries of the same kind in the item's workspace and browses their folders with `GET /v1/resources/{id}/children?kind=folder`, 100 at a time.
- The collection shows one level of children at a time.
- Preference language and number-format choices include tags the server may reject. The server accepts a BCP-47 tag whose base language matches with exact confidence. A rejected value returns 422.
- Columns chosen on the page are the `cols` URL parameter. With a saved view they are the query's `select`; managers save them, with the column sort, to a table view (`PUT /v1/views/{id}`) or a new view.
- Plain browsing reads `GET /v1/resources/{id}/children` of the open folder or collection. A saved view, a column sort or a column filter uses `POST /v1/resources/{id}/query` with the view's query, the open folder as `parent_id`, the search box as `search`, the chosen sort and the column filters; the search box replaces a view's own search text. Search matches whole words.
- Smart folder create stores a name, an owner (personal or one workspace), folder inclusion and one comparison with a text, number or boolean value. The group path stays in page state.
- Inbox counts on Home come from `POST /v1/resources/{id}/query` with `include_total`. That total can include folders.
- Column filters narrow what the open view (or plain browsing, or a search) shows: each table header's funnel, and the Filter button for any column and for board, gallery and calendar layouts, filter one column in the way its type suits. They are the `filters` URL parameter (one entry per column, so they survive paging, sorting and reloading and can be shared), become conditions ANDed into the view's own filter (joining its top-level AND, so they never weaken it), and show as chips with Clear filters. People who manage the collection save them into the view (`PUT /v1/views/{id}` with the combined filter, or with the columns from the column picker); everyone else filters for themselves. Navigation views take none: the server runs their saved query, and `/v1/views/{id}/query` has no ad hoc filter. The page checks the API's 32 nodes and depth 6 before applying a filter.

  | Column type | Filter | Query conditions |
  | --- | --- | --- |
  | `$name` | contains, is exactly | `contains`, `eq` |
  | text, email, URL | contains; is one of (the column's distinct values among what the page shows, from `POST /query/groups`, with counts and a server-side search, and "No value"); is exactly; is empty; has a value | `contains`, `in`/`eq` (`or` with `missing`), `eq`, `missing`, `present` |
  | integer, decimal, number, currency | is between (both ends included, either open); is exactly; is empty; has a value | `gte`/`lte` with exact numbers (decimals as text), `eq`, `missing`, `present` |
  | date | is between days (periods: today, yesterday, this/last week from Monday, this/last month, this/last year, last 7 or 30 days, or a month); is a day; is empty; has a value | `gte`/`lte` on days |
  | date-time, `$created_at`, `$modified_at`, `$archived_at` | as dates, the days read in the person's time zone; built-in dates offer no empty filters | `gte` the first day's start and `lt` the start of the day after the last, as UTC instants |
  | choice (single or multiple) | is one of / is none of its choices, and "No value" | `in` (any member of a multiple choice), `not`, `missing` |
  | boolean | Yes, No, "No value" | `in`, `missing` |
  | person, `$created_by`, `$modified_by`, `$archived_by` | is one of / none of the people picked, "Me", and "No value" for person fields | `in`, `eq` with `value_ref: me`, `missing` |
  | lookup | is one of / none of the items picked from the looked-up list, "No value" | `in`, `missing` |
  | term | is one of / none of the terms picked, "Include sub-terms" (always, on a field that applies parents), "No value" | `in`, one `under` per term, `missing` |
  | keywords, `$keywords` | is one of / none of the keywords picked, "No value" | `in` (`$keywords`: one `eq` per keyword), `missing` |
  | note | none: notes have no index rows | — |
  | a field that is not indexed, or whose index is building or failed | none; the header says how to make it filterable | — |

  Value lists hold at most 50 values per `in`; longer selections become several. A quick period fills in its days when chosen, so a saved filter keeps those days; the views editor's relative dates stay the way to save a rolling period.
- Without a saved view, a table shows the name, the first four columns of the default content type and the modified date. Column headers sort by the name, dates, people and filterable (indexed, ready) single-value columns; the sort is the `sort` URL parameter and replaces the view's sort. Folders sort among items.
- Table cells and cards name people from `GET /v1/identity/users` (when the caller can read users), terms and keywords from `GET /v1/terms?ids=` (when the caller has `taxonomy.read`) and looked-up items through `$batch` reads, 20 per request. Unreadable looked-up items read "Unavailable item".
- `GET /v1/people` returns subject strings. Display names come from `GET /v1/identity/users` when the caller has `user.read`.

## What the UI matches

- Actions follow `access` on `GET /v1/resources/{id}`: adding, editing, moving and deleting need `write`, publishing `publish`, settings and permissions `manage`. Selection and bulk actions use the list's access; an item with its own stricter permissions can still refuse them. Creating workspaces needs the `workspace.create` scope.
- Resources are `/v1` and JSON fields are snake_case. Bearer deployments send `Authorization: Bearer`; proxy deployments use same-origin browser HTTP credentials. `/auth/config` selects the supported mode, and the OIDC client for provider sign-in, before the app mounts.
- Live updates use `fetch` on `GET /v1/events`. The stream uses the same bearer or proxy credentials as other requests.
- A personal home workspace is `personal_owner_id`. The sidebar hides it. Home and Inbox still open it through `GET /v1/identity/me/home` and `GET /v1/identity/me/inboxes`. Global search (`GET /v1/search`) covers it with the other readable workspaces; the search page shows hits with snippets and workspace, collection and tag facets.
- Preference writes send `If-Match`, including `"0"` when the user has no saved row. A stale preference write is 409.
- One organization per deployment.
- The API rejects a new relationship whose items are in different workspaces.
