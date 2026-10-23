# PaperGo

DMS and CMS application with its React frontend and Go API in one executable. Its **DAL** uses **Bob** with authoritative **SQLite** and **PostgreSQL** schema files. One organization per deployment, with multiple workspaces. The API is a modular monolith with immutable content/schema revisions, derived read projections, and local immutable blob storage. [Architecture decisions](docs/architecture.md) explain the extension boundaries. [Application foundation](docs/application-foundation.md) documents rich fields, templates, views, taxonomy, typed relationships, and exclusive permission scopes. [Search](docs/search.md) finds items and folders across workspaces, trimmed by permissions. [Workflows](docs/workflows.md) are how anything happens after a change or on a schedule: triggers, conditions and flows of activities that people can change, including the built-in processes PaperGo ships, run durably by an embedded runner. [Operations](docs/operations.md) is the lifecycle all background work shares: claims, attempts, parking, cancellation, ownership and retention. The optional [MCP server](docs/mcp.md) lets AI assistants discover, query, search, read, create and update items with the connecting user's access.

## Install from a release

Download the ZIP for your platform and `papergo_vX.Y.Z_assets.zip` from the same release. Extract the platform ZIP, then extract the shared ZIP into that directory. The resulting `assets/` folder belongs beside `papergo` (or `papergo.exe`) and `config.yaml`:

```text
papergo/                 # your installation directory
  papergo[.exe]
  papergo-admin[.exe]
  config.yaml
  lib/                   # Linux native libraries; Windows DLLs sit beside the EXEs
  notices/
  assets/
    models/
      paddleocr/
      laya/
      embedding/
        embeddinggemma-2/
        harrier-oss-v1-0.6b/
    notices/
```

Run the executable from that directory and open http://localhost:8080. The first start creates the database automatically. Node.js and Python are not needed at runtime. Linux amd64/arm64 and Windows amd64 release builds are enabled; macOS builds are temporarily disabled.

The binary ZIP includes the web UI, database schema, native libraries, compiler runtimes and their license notices. The shared ZIP contains PaddleOCR, Laya FP32, EmbeddingGemma 2 and Harrier Q4 models, tokenizers and model notices, and works with every platform. Native libraries and models are read directly from the installation; model paths are resolved beside the executable even when started from another working directory. The web UI stays embedded in the executable. OCR and local workflow decisions use the shared models automatically. Set `AI_EMBEDDINGS_PROVIDER=local` and select `AI_EMBEDDINGS_MODEL=embeddinggemma-2` (default) or `harrier-oss-v1-0.6b` for local semantic search. Inference never downloads models. `ONNXRUNTIME_LIB`, `OCR_MODELS`, `AI_DECISION_MODELS_PATH` and `AI_EMBEDDINGS_MODELS_PATH` select explicit alternatives. Linux builds require the target system's glibc.

Configuration is optional. The sample `config.yaml` listens on port 8080, signs in a local administrator (`admin` / `admin`), and stores the database, files and zvec index under `./data`. Set the initial password before the first start. Set `auth.mode` to `oidc` or `proxy` when an identity provider or trusted proxy should sign people in. Non-empty environment variables override matching settings in `config.yaml`. Release archives include the optional configuration, operator documentation and the `papergo-admin` maintenance tool, which shares the native libraries.

See the [sample YAML configuration](deploy/config.yaml) for the supported sections. Unknown fields, duplicate keys and multiple YAML documents are rejected. Quote password values; `auth.admin_password: ""` or `null` prompts securely on an interactive first start and fails on a non-interactive one. Proxy configuration uses a YAML list for `auth.proxy_trusted_cidrs`.

## Run locally

[Self-hosting performance](docs/performance.md) records measured CPU, memory,
query latency and storage costs, sizing estimates, and a reproducible load test.

Building requires Go 1.27+ (the [Dockerfile](Dockerfile) pins the exact toolchain CI and the Docker build use), Node.js 24 with npm, Python 3.11+, a C compiler, CMake 3.26+, pkg-config. Windows also needs the Rust GNU toolchain for its static tokenizer library. Optional local tools live under `.tools/`, which is ignored by Git. No Docker or database service is required for native development.

For a ready-to-use Linux development environment, open the repository in VS Code
and run **Dev Containers: Reopen in Container**. The [development guide](docs/development.md)
covers the pinned toolchain, automatic setup, hot reload, debugging and checks.
For rootless Docker on Linux, use **Dev Containers: Clone Repository in Container Volume**.
With tools installed on Linux or macOS, run `bash scripts/setup-dev.sh`, then
`bash scripts/dev.sh`; `bash scripts/check.sh --packages ./internal/dms` runs
cached checks for a selected Go package. `scripts/ci.sh` runs the pull request
checks exactly as CI does, in its toolchain image, with only Docker installed. See
[development checks](docs/development.md#check-changes) for every stage.

PowerShell, from the repository root:

```powershell
./scripts/dev.ps1
```

The dev script builds the frontend, generates a bearer token, prints it once, and starts the UI and API on `http://127.0.0.1:8080`. Supply `-Token` to reuse your own token. `.env.example` documents configuration. The process reads `PAPERGO_CONFIG` or `./config.yaml` when that file is present, then applies environment variables. It does not automatically load `.env` files.

With globally installed tools, run `npm ci --prefix web` and `npm --prefix web run build`, then `go run ./cmd/api`. With no configuration file the server listens on `http://127.0.0.1:8080`, uses local authentication (user `admin`, password `admin`) and the zvec search store, and creates the current schema in an empty data directory. Set `AUTH_MODE=development` and `DEV_TOKEN` to a random token of at least 32 characters when you want the development bearer token instead. Building the server, or running Go tests and analysis across `./...`, requires the frontend build in `web/dist` because it is embedded at compile time.

```powershell
./scripts/check.ps1
```

Tests initialize temporary databases from the DAL schema masters and verify repository invariants on SQLite and PostgreSQL. Pull requests run the lint and test stages; pushes to main, a nightly schedule and releases add the Go race detector, real models, native search, packaging, the container image and the dependency vulnerability scan. The Docker smoke test runs the built image directly with production authentication, a persistent data volume and a read-only root filesystem, and checks the UI, health, graceful shutdown, native search, PDFs, OCR, Laya and both embedding presets. `scripts/ci.sh docker` builds the image and runs it.

The machine-readable REST contract is in [`api/openapi.json`](api/openapi.json).

## Web UI

The React app in [`web/`](web/) is embedded in the executable and served at `/` on the same port as `/v1`. Browser routes such as `/admin/users` load the app directly. Hashed assets are cached for a year; HTML and unhashed public files revalidate. Missing assets return 404.

To build the deployable executable from the repository root:

```sh
bash scripts/build.sh
```

On Windows, run `./scripts/build.ps1`. These scripts install the locked frontend dependencies, build the UI, then run `scripts/build-binaries.py` and `scripts/build-assets.py`. The `bin/` directory contains both executables, their native libraries and `assets/`; copy the whole directory to deploy it. Preparing Laya from scratch requires Docker for the pinned Linux CPU exporter, cached under `.tools/decision/model`. To use an existing verified Laya directory/archive or HTTPS artifact, run `python3 scripts/prepare-preset-models.py --laya-bundle PATH_OR_URL` before building. Asset preparation also downloads and verifies both embedding presets and their model cards. Binary compilation alone needs no model downloads: `python3 scripts/build-binaries.py`. For local Go commands, first run `python3 scripts/native.py` and source `.tools/native/env.sh` (PowerShell: `. .tools/native/env.ps1`). The Docker image uses the same executable, native-library and shared-assets layout under `/opt/papergo`.

For frontend development with hot reload, run this from `web/` alongside PaperGo:

```text
npm ci
npm run dev
```

Vite listens on `http://127.0.0.1:5173` and proxies `/v1`, `/auth` and `/health` to `http://127.0.0.1:8080`. Set `PAPERGO_API` to point the proxy somewhere else. In development, sign in with the bearer token printed by the dev script. Trusted-proxy deployments use the browser’s HTTP authentication prompt and load the verified profile automatically. Screens the API does not support, and API areas that do not have a screen yet, are listed in [the web frontend backlog](docs/plans/web-frontend-backlog.md).

[Organization groups and roles](docs/identity.md) are managed under `/v1/identity`. OIDC deployments require `ADMIN_SUBJECT` or `ADMIN_GROUP` for the first administrator; proxy deployments require `ADMIN_SUBJECT`; development defaults to `DEV_SUBJECT`. Verified subjects enroll with the Member role on first authentication. User profiles, guarded anonymization, organization metadata, scoped personal access tokens and admin-managed integration tokens are exposed by these APIs. Module scopes and resource ACLs apply independently, including to owners and workflow item actions.

## Data model and requirements

| Requirement | Implementation |
| --- | --- |
| Workspaces, lists, libraries, folders and items | One typed resource hierarchy. A workspace contains lists/libraries; those contain folders/items. Items have one current list/library container and can move across compatible collections/workspaces through REST; folders move within their collection. Library folders and items carry file names, unique among siblings ignoring case. |
| Delete folders and items | Delete an item or empty folder, or explicitly delete a folder and its contents with `recursive=true`. Recursive deletion requires write access to the whole tree and commits atomically. Tombstones retain revisions, publications and audit history. |
| WebDAV access to libraries | Optional per library (`webdav_enabled`). Windows Explorer, macOS Finder and Office can browse, open, create, overwrite, rename, move, copy, lock and delete files and folders, with the same permissions, publishing and history as the REST API. |
| Flexible SharePoint-style fields | Per-container typed fields and immutable schema revisions. Types: text/note/email/url/date/datetime, choice, number/integer/decimal, boolean, lookup, term and keywords (by term ID or label); defaults, bounds, length limits, and indexed multi-values. Unknown fields, wrong types and missing required values are rejected. |
| Library as blob plus list item | A library item has the same metadata and permission behavior as a list item, plus immutable blob revisions stored outside SQLite. |
| Create a document in one upload | Streaming multipart REST creation accepts a file and metadata, including type, folder, values, keywords and language hints, and commits the first revision with content atomically. |
| Control duplicate uploads | Libraries configure Allow/Warn/Block policies based on file hashes and current documents visible to the caller across workspaces. |
| Restore an earlier file | Restore retained bytes and filename as a new file version while retaining current metadata and following publication settings. |
| Unique permissions at every level | SharePoint-style inheritance: only the nearest exclusive scope supplies additive read/read_draft/write/publish/manage grants. Break, copy, or reset inheritance explicitly. Manage implies all actions; write/publish imply draft and published read. Denies are rejected. |
| Typed item relationships | Every link has a workspace relationship type supplying its key, directed/symmetric policy, validated attributes, cardinality limits, and edge ETags. Indexed incoming/outgoing queries require visible, authorized endpoints before pagination. New cross-workspace links are rejected; item moves preserve existing links, which require readable live endpoints. |
| Publish any list/library item | Publishing disabled by default: new items, edits and uploads are automatically published. With publishing enabled, changes create drafts and explicit publish/unpublish controls the published pointer. Ordinary readers see published revisions only. Library revisions pin their blob. |
| Preserve content history | Every item create, metadata edit and upload stores an immutable revision tied to its schema revision. Head and published surfaces are independent in publishing collections; without publishing they are one surface. Lifecycle/ACL changes advance the lock version without creating content revisions. |
| Keywords on all content | Shared folksonomy with chips, autocomplete and keyword filters. Intrinsic keywords and keyword fields store term IDs from the Keywords term set in Taxonomy. Workspace browsing counts readable resources on the selected content surface. |
| Read-heavy browsing and search | Keyset pagination and permission filtering before LIMIT; an optional search index kept current by a workflow, and keywords, on the selected head/published surface; optional typed field indexes for exact filters and ranges. Multi-step reads use a consistent database snapshot. |
| Enterprise backend foundation | Fail-closed bearer authentication, production OIDC, transactional audit events, optimistic concurrency, strict JSON, upload limits, structured logs, health probes, graceful shutdown and non-root container build. |
| Reusable application schemas | Organization content-type and field catalogs, live collection attachments, independent defaults, cross-field rules and templates; shared updates advance immutable effective schemas atomically. |
| Business keys | Indexed scalar uniqueness across head and published values, enforced transactionally for single and bulk writes. |
| Saved views and controlled taxonomy | Bounded typed queries, sorting, pagination, opt-in authorized totals, grouping; an organization-wide governed taxonomy of term groups, sets and hierarchical terms (labels, synonyms, colors, order, deprecation, move, merge, SharePoint CSV import, portable packages) and keywords people add, which managers promote into managed sets. Workflows can react to terms gained or lost. |
| File previews | Asynchronous first-page WebP renditions under 100 KB, with authorized viewing and original downloads. See [previews](docs/previews.md). |
| Smart folders | Personal or shared saved searches and classification folders, visual typed rules with live previews, direct table/grid results with collection badges, and optional metadata groups. See [smart folders](docs/smart-folders.md). |
| Notifications | An inbox per person, a bell with the unread count and live updates. Tasks tell their assignees and creators, failed runs tell their workflow's author, and people and workflows send messages, reaching only people who can read what they concern. See [notifications](docs/notifications.md). |
| Customizable automation | Workflows are TypeScript scripts that react to item changes, schedules, manual starts and each other's runs, with conditions in the query filter language, and act through the REST API. Versions are immutable; runs are durable operations whose API requests write to the database once, act with their author's permissions, wait for long work without holding a worker, and start from a durable event log written with the change that triggers them. Built-in workflows ship core processes that people configure, turn off or copy. See [workflows](docs/workflows.md). |

## Archiving

Documents and list items can be archived without moving them or changing their properties. Normal browsing and search exclude archived items; the global Archive page and each collection's permanent Archived view make them available. Archived items remain editable. See [archiving](docs/archiving.md) for permissions, API actions, search consistency, and WebDAV behavior.

## API

Every `/v1` request requires authentication: HTTP Basic or a session cookie in local deployments, `Authorization: Bearer <token>` in bearer deployments, or the trusted-proxy identity contract in proxy deployments. Local browser sessions are created by `POST /auth/login` and revoked by `POST /auth/logout`; HTTPS deployments set Secure cookies. The web UI, `/auth/config`, local sign-in endpoints and health endpoints are public at the application server; the NPM proxy sample protects the UI and API with its Access List. IDs are UUID strings. Lists of resources and relationships return `{ "value": [...], "@odata.nextLink": "..." }`; follow `@odata.nextLink` to continue; it is null on the final page. Use `$top` to select a page size (default 50, clamped to 1–500); links contain opaque `$skiptoken` continuations. POST query continuations use the same method and body.

`POST /v1/$batch` executes 1–20 independent HTTP calls in order. Send `{"requests":[{"id":"list","method":"GET","url":"workspaces"}]}`. Each `responses` entry contains its ID, status, headers and optional body. Calls inherit authentication and may override request headers. `dependsOn` names earlier calls; failures skip dependents with 424. Successful writes persist independently. Relative API URLs only; nested batches and path traversal are rejected. Non-JSON string bodies become raw text; binary responses become base64 strings, with a 16 MiB limit per result.


| Method | Route | Purpose |
| --- | --- | --- |
| POST / GET | `/v1/workspaces` | Create a workspace / list accessible workspaces |
| GET / PATCH / DELETE | `/v1/resources/{id}` | Read / update names, keywords, item values, `parent_id` (move) and collection settings / delete a folder or item |
| POST / GET | `/v1/resources/{id}/children` | Create / browse children; `kind=folder` (or another kind) lists only that kind |
| POST | `/v1/resources/{id}/bulk` | Atomically create, update/move, publish, unpublish or delete up to 100 items in one list/library |
| GET | `/v1/workspaces/{id}/keywords` | The keyword vocabulary with counts of readable resources; `collection_id`, `prefix`, `$skiptoken`, `$top` |
| POST / GET | `/v1/smart-folders` | Create / browse personal and shared query definitions |
| GET / PUT / DELETE | `/v1/smart-folders/{id}` | Read / replace / delete a definition with its own ETag |
| POST | `/v1/smart-folders/preview` | Preview unsaved rules, five authorized matches and bounded count |
| POST | `/v1/smart-folders/{id}/query`, `/query/groups` | Query live membership / navigate metadata groups with authorized counts |
| POST | `/v1/smart-folders/{id}/items` | Classify an existing item or create a match in classification mode |
| DELETE | `/v1/smart-folders/{id}/items/{itemID}` | Remove classification while preserving the item and location |
| GET / POST | `/v1/workspaces/{id}/smart-folders/export`, `/import` | Export / atomically merge portable shared definitions |
| GET | `/v1/items/{id}/text` | Text read from the item's file, page by page; see [documents](docs/documents.md) |
| GET / POST | `/v1/search` | Global search across readable workspaces, with field filters, facets and relevance order; see [search](docs/search.md) |
| GET | `/v1/resources?workspace_id=...&q=...&keyword=...` | Workspace search and keyword filtering; optional `parent_id` narrows to direct children and `kind` to one kind of resource |
| POST / GET | `/v1/resources/{id}/fields` | Add / read list or library field definitions |
| PATCH | `/v1/resources/{id}/fields/{fieldID}` | Evolve label, choices, required or indexed; uses the container ETag |
| GET | `/v1/resources/{id}/schemas` | Manager-only schema history; paginate with `$skiptoken` |
| PUT / GET | `/v1/resources/{id}/permissions` | Replace / read explicit ACLs and inheritance setting |
| POST / GET | `/v1/items/{id}/relationships` | Create / query typed or untyped edges across readable workspaces; default and `both` include all incident edges, `incoming` / `outgoing` select directed edges |
| GET / PATCH | `/v1/items/{id}/relationships/{linkID}` | Read a located peer and edge ETag / update attributes |
| GET | `/v1/workspaces/{id}/relationships` | Query edges with readable endpoints, direction and scalar attribute filters |
| DELETE | `/v1/items/{id}/relationships/{linkID}` | Remove an incident relationship without an ETag |
| POST / GET | `/v1/items/{id}/publications` | Publish / read immutable snapshots; paginate with `$skiptoken` |
| POST | `/v1/items/{id}/unpublish` | Remove the published surface while retaining history |
| GET | `/v1/resources/{id}/delta` | Synchronize visible items/folders with durable caller-bound delta links, removals and access changes; anchors last 30 days, continuations five minutes; expired tokens return 410 |
| GET | `/v1/items/{id}/revisions` | Retained content history for item readers; paginate with `$skiptoken` |
| GET | `/v1/items/{id}/schema?surface=auto` | Immutable schema of the caller's visible content revision |
| PUT / GET | `/v1/items/{id}/content` | Upload a blob / download the selected revision's blob; draft readers may request historical `blob_id` |
| POST | `/v1/resources/{id}/documents` | Create a library document using multipart `file` and optional `metadata` JSON; see [documents](docs/documents.md) |
| GET | `/v1/items/{id}/content/versions` | Retained file versions, newest first: number, file name, type, size, SHA-256, who added it, and the current one. Ordinary readers see only the published file |
| POST | `/v1/items/{id}/content/versions/{number}/restore` | Restore an earlier file as a new version; requires current numeric `If-Match` |
| GET | `/v1/audit` | Organization-wide feed requiring `audit.read`; actor/entity/workspace/date filters, changed properties, trace IDs and newest-first paging |
| GET / PATCH | `/v1/identity/me` | Current user profile and self display-name edits |
| GET / PATCH | `/v1/organization` | Organization identity and profile; edits require organization.manage and ETag |
| POST / GET | `/v1/identity/me/tokens` | Issue/list scoped expiring personal access tokens |
| DELETE | `/v1/identity/me/tokens/{id}` | Revoke a personal access token |
| WebDAV | `/webdav/{libraryID}/...` | A WebDAV-enabled library as a network drive; see [WebDAV](#webdav) |
| GET | `/health/live`, `/health/ready` | Process liveness / database and migration readiness |
| GET | `/v1/storage/status` | Main-file allocation, passive journal checkpoint, search and distinct blob bytes; requires organization.manage |

Read/update responses carry a quoted numeric `ETag`. PATCH, resource DELETE, catalog PUT, view DELETE, template application, ACL replacement, blob upload, publish and unpublish require `If-Match: "<current version>"`. Missing preconditions return 428; stale versions return 412. These mutations consume a lock version; publishing an already published head returns 409. Content `revision_number`, blob `version`, and resource lock `version` are independent counters. Upload returns `resource_version` and an ETag for the resource. Publication event `version` records the lock version consumed by the transition. PATCH `values` merges supplied keys; null removes an optional value. Relationship deletion requires write access to both endpoints and does not require If-Match.

GET and PATCH `/v1/resources/{id}` add `access`: the actions the caller may take on that resource, in the order `read`, `read_draft`, `write`, `publish`, `manage`. It applies the same ACL and module-scope rules as the requests themselves (`write` and `publish` below a workspace also need `item.write`, and `manage` needs `list.manage`), so clients can offer only allowed actions. The server still checks every request.

GET resources and browsing support `surface=auto|head|published`. Auto chooses head for draft readers and published for ordinary readers. An unpublished item returns 404 to a reader; explicit head requests require draft access. Names, keywords, values, search matches, field filters and downloads all use the chosen surface. Readers cannot download draft or arbitrary historical blobs. A published resource's lock version may advance when editors save drafts; its `updated_at` describes the selected content revision.

Currency pickers accept codes, symbols and names. New currency fields preselect the workspace `default_currency`, or the organization preference (USD by default). Workspace managers can set or clear the override; existing field definitions retain their currency.

Fields use stable immutable keys, types, decimal scales, multiplicity, and reference scopes. Multiple values are supported only by choice, person, lookup, term and keywords. Person values store active enrolled organization subjects; GET `/v1/people` lists eligible IDs with `item.read`. Labels, choices, required and indexed flags can evolve; existing revisions retain their original schema. Adding a required field to a populated collection requires a valid default; setting an existing field required affects future edits. Set `indexed:true` to enable typed queries. Changing this flag queues a background index build; the field is queryable once its `index_status` is `ready`. For example, `GET /v1/resources/{listID}/children?filter_field=amount&filter_op=gte&filter_value=125.00`. Operators: `eq`, `gt`, `gte`, `lt`, `lte`; boolean supports only `eq`. Filters require a list/library/folder scope and apply to direct children.

Read an item's `/schema` for the schema paired with its selected revision, its visible `item` system properties and optional `file` metadata (display filename, media type and byte size). Field inspectors separate system properties and intrinsic keyword assignments from custom fields. Revision history includes each immutable schema in `schema_revision`, allowing item readers to interpret historical values without collection management access. The collection `/fields` endpoint describes current configuration. Collections accept `versioning:"off"|"major"` and `max_versions` (1–500, default 50) on creation and PATCH with manage access and `If-Match`. Lists and libraries default to history off. Turning history off stops new entries while preserving retained history. Each recorded change trims that item to its configured limit. Trimmed snapshots cannot be read or restored through history; snapshots remain only while referenced by current heads, publications, retained history or undispatched event processing. Unreferenced metadata snapshots are collected. History is newest first. Its `revision_number` increments only for recorded entries, independently of internal content snapshot numbers; pausing history creates no numbering gaps. `is_current` identifies the newest retained record, including while recording is off.

`integer` accepts exact JSON integers within signed 64 bits. `decimal` accepts a decimal string or exact JSON number, with fixed `scale` from 0 to 9; it stores a canonical string in content and a signed 64-bit scaled integer in indexes. Excess fractional digits and overflow are rejected without rounding. Use strings for decimals and an exact-number parser in clients; JavaScript clients should avoid unsafe numeric literals above 2^53. `number` is an approximate floating-point query type. Optional null values produce no field-index row.

Example request bodies, in order:

```json
{"name":"Engineering","keywords":["internal"]}
```

POST the workspace ID's `/children` to create a library:

```json
{"kind":"library","name":"Contracts","keywords":["legal"],"publishing_enabled":true}
```

POST the library's `/fields` (requires manage):

```json
{"key":"status","label":"Status","type":"choice","required":true,"choices":["draft","approved"],"indexed":true}
```

POST the library's `/children`:

```json
{"kind":"item","name":"Supplier agreement","values":{"status":"draft"},"keywords":["supplier"]}
```

Upload bytes to the item's `/content` with `Content-Type: application/pdf`, `X-Filename: agreement.pdf`, and `If-Match: "1"`. PDF, TIFF, JPEG, PNG and WebP are accepted by their actual byte signatures; filenames and declared media types do not select the stored type. Unsupported content returns 415 without a new revision. The same policy applies to multipart creation, WebDAV PUT and package import. Upload returns blob metadata and the new item ETag; GET supports Range requests and always sends an attachment disposition. Publish with POST `/publications` and the new `If-Match` value.

When publishing is disabled, library metadata is visible immediately, including before the first upload; downloading returns 404 until a blob exists. Explicit publication in an enabled library requires a blob. Enabling publishing keeps existing published content and makes future changes drafts. Disabling it requires manage access and publishes all current heads atomically, advancing affected item lock versions.

Break item permission inheritance using PUT `/permissions` and its current `If-Match`:

```json
{"inherit":false,"grants":[{"principal_type":"user","principal_id":"local-admin","action":"manage","effect":"allow"},{"principal_type":"user","principal_id":"reviewer-subject","action":"read","effect":"allow"}]}
```

Reset with `{"inherit":true,"grants":[]}`, or break and copy with `{"inherit":false,"copy_inherited":true}`. Inherited resources cannot carry local grants. GET permissions identifies the effective scope and grants. An ACL replacement that removes the caller's manage access is rolled back. A workspace creator receives manage access atomically and needs `workspace.create`. User principals identify enrolled organization subjects; group principals identify organization groups, including nested members. Persisted module roles apply independently of content ACLs. The shared organization term store uses `taxonomy.read` for reads/open contributions and `taxonomy.manage` for curation, without requiring workspace membership.

Create a relationship type in the workspace (`POST /v1/workspaces/{id}/relationship-types`), then link items with it:

```json
{"type_id":"<relationship-type-id>","target_id":"<another-item-id>","metadata":{}}
```

The link takes its name, direction, attribute schema and cardinality from the type.

ACL and publication visibility filtering happen before LIMIT, so hidden rows neither shorten pages nor supply cursors. Search in listings treats input as a literal phrase whose last word is a prefix, uses Unicode tokenization, and returns the listing's stable order, not relevance order; [global search](docs/search.md) ranks by relevance. The index holds metadata and published file text from the content workflow. Browsing a scope requires read access to that scope; explicitly granted items remain accessible directly by ID. Relationships are live, separately audited edges; publication does not freeze a relationship graph.

Move a folder or item with PATCH `{"parent_id":"<folder or collection id>"}`: the target must be in the same list or library, not below the moved folder, and writable by the caller. A move consumes a lock version but creates no content revision; inheriting resources take the new location's permission scope. DELETE `/v1/resources/{id}` with `If-Match` deletes an item or empty folder with write access. Use DELETE `/v1/resources/{id}?recursive=true` to delete a folder and all its contents in one transaction; the caller needs write access to every live descendant. A permission failure or stale version leaves the whole tree unchanged. Without `recursive=true`, any live child, including an unreadable item or nested folder, prevents folder deletion with `409 folder_not_empty`. Deleted children do not prevent ordinary deletion. Workspace owners and collection managers can delete their respective resource trees. Deletion hides relationships, emits an `item.deleted` event for each deleted item and records a `resource.delete` audit event. [Recycle bins](docs/recycle-bin.md) restore deleted items/folders and support manager purge with durable file cleanup. [Cross-collection moves](docs/item-moves.md) preserve item identity, content and history while inheriting destination access. Items can change attached content types through PATCH; unused types can be detached with collection management access. Caller-ID item/folder creates return 201 initially and 200 unchanged on a readable same-collection retry.

## WebDAV

A library created with `"webdav_enabled": true`, or updated to it by a manager, is also served at `https://<host>/webdav/<libraryID>/` (RFC 4918 class 1 and 2). Its folders are collections and its items are files whose bytes are the caller's visible revision: head for draft readers, published for ordinary readers. Every request runs through the same service as the REST API, so permissions, publishing, validation, revisions and audit events are identical. Libraries without the flag and paths the caller cannot read return 404.

In proxy mode, WebDAV clients use their NPM Access List username and password over HTTPS. In local, OIDC and development deployments, users create a personal access token in **Settings → WebDAV** or with `POST /v1/identity/me/tokens` and `{"name":"Laptop","scopes":["item.read","item.write","document.read","document.write"],"expires_in_days":90}`. Request only scopes your account holds; read-only access needs `item.read` and `document.read`. Windows Explorer and macOS Finder use the token secret as their Basic password with the account subject as username. WebDAV also accepts the token as a Bearer credential, or an OIDC provider bearer token. The response shows `secret` once; only its SHA-256 is stored. Each user can hold 100 active personal tokens, and `DELETE /v1/identity/me/tokens/{id}` revokes one.

To connect from Windows, choose **Map network drive** in Explorer and enter `https://<host>/webdav/<libraryID>/`, or run `net use Z: https://<host>/webdav/<libraryID>/ /user:me <password>`. Windows sends Basic credentials only over HTTPS unless `BasicAuthLevel` is set to `2` under `HKLM\SYSTEM\CurrentControlSet\Services\WebClient\Parameters`, which local HTTP development needs (restart the WebClient service afterwards). The Windows client also rejects files above its `FileSizeLimitInBytes` (50 MB by default); the server limit is `MAX_UPLOAD_BYTES`.

| WebDAV | Effect |
| --- | --- |
| PROPFIND | Depth `0` or `1`; `infinity` is refused with `propfind-finite-depth`. A folder lists at most 10,000 entries. |
| GET / HEAD | Download the visible content, with Range and conditional requests. |
| PUT | Create an item whose first revision holds the content, or add a content revision to an existing file. `If-Match` and `If-None-Match` are honored. |
| MKCOL | Create a folder. |
| MOVE | Rename or move within the library in one transaction, replacing the destination unless `Overwrite: F`. Destinations in other libraries return 502, so clients copy and delete instead. |
| COPY | Create new items carrying the source's bytes, keywords and field values; copying onto an existing file adds a content revision. COPY and MOVE refuse (403) a destination that is the source itself, contains it, or lies inside it. |
| DELETE | Delete a file or a folder and its contents atomically, requiring write access to every descendant. |
| LOCK / UNLOCK | Exclusive or shared write locks of at most one hour, refreshable, held in memory and released on restart. |
| PROPPATCH | Live DAV properties are refused; others, such as Windows timestamps, are acknowledged but not stored. |

New files take the library's field defaults; a library with a required field that has no default refuses new files with 403 and the reason. In a library with publishing enabled, writes are drafts: readers keep the published names and bytes until an editor publishes. File names follow the library rules above, and each saved version (Windows writes an empty file before its content) becomes a content revision.

## Schema changes

The authoritative database schemas are `internal/dal/schema/sqlite.sql` and
`internal/dal/schema/postgres.sql`. Edit them directly and keep their logical
contracts aligned. SQL, query builders and database initialization belong to
`internal/dal`; the rest of the application calls typed repositories.

Bob models are generated from the schema masters, the PostgreSQL ones in a
temporary container, so Docker must be available:

```sh
bash scripts/generate-models.sh
```

Do not add development migrations, backfills or compatibility paths. Local
databases are disposable. Initialization installs the selected embedded master
and records its fingerprint; existing databases must match that fingerprint.
Published release bundles are described in [release management](docs/releases.md).

## Tagged binary releases

Push a version tag such as `v0.1.0` to run verification, build and smoke-test Windows and Linux executables, publish archives with checksums to GitHub Releases, and publish Linux amd64/arm64 images to `ghcr.io/eduardpaul/papergo`. Stable releases update the `latest` image tag; release candidates use only their version tag. Release-only database changes are embedded in the executables; stop the service, replace the binaries and restart to apply them automatically. Feature development keeps its single editable schema. See [release management](docs/releases.md) for platform support, release candidates, blocked changes, backups and recovery.

## Deployment and scaling

Use one API process with a local persistent disk. SQLite runs WAL mode, foreign-key enforcement and FULL synchronous durability. Each connection starts a passive checkpoint after 256 pages. The next write after that checkpoint finishes retains about 1 MiB of journal. A large transaction keeps the frames it wrote, and a blocked checkpoint leaves the journal at its high water until a later write. `GET /v1/storage/status` reports that progress to organization managers, together with main-file bytes, page and freelist counts, per-table allocation, unused bytes inside allocated pages, extension stores such as search, distinct available blob bytes, run counts, and rows retention can collect. The route does not delete those rows. `dbstat` omits freelist pages, so accounted bytes plus freelist bytes equal `page_count` times `page_size`. That is the main file size when the checkpoint finishes; a blocked checkpoint can leave newer pages only in the journal. Unused bytes are slack inside pages that still belong to a table or index. A fresh database is mostly that slack. Blob bytes count each available SHA-256 once and live in the object store, so database size is not a function of uploaded file count. `PRAGMA wal_checkpoint(TRUNCATE)` during a quiet period, with no long reader connected, reclaims the journal file and leaves the main database file as it is. `VACUUM` rewrites the main database; PaperGo does not schedule it. Write transactions take the write lock when they begin and wait up to 30 seconds for it, so API requests and workflow steps share the single writer; an in-process mutex keeps request writes in order, and WebDAV locks live in the same process; readers use a bounded connection pool. Workflow runs execute inside the API process as operations of the same database file; `RUN_RETENTION` sets how long finished runs are kept (default `720h`). `SYSTEM_RUN_RETENTION` sets successful automatic system runs (default `24h`, or `RUN_RETENTION` when that is shorter). `RESULT_CACHE_TTL` sets the idle lifetime of reusable answers (default `720h`) and stays as configured when `RUN_RETENTION` changes. Retention runs hourly, so an eligible row can wait up to one hour. Audit events and publication snapshots are kept. Search is optional: `SEARCH_STORE=none` runs without it, and lists then match search text by name. Its index uses `SEARCH_STORE=zvec` by default: native full-text/HNSW indexes at `SEARCH_ZVEC_PATH` (default `search/zvec` beside the database), with the library included in the executable's archive and the Docker image. `SEARCH_STORE=sqlite` selects the SQLite search file at `SEARCH_DATABASE_PATH` (default `search.db` next to the database). Both rebuild from the database when missing; `SEARCH_LANGUAGES` (default `english`) sets the languages whose word stems it indexes ([search](docs/search.md)). Do not run multiple API replicas against a shared network filesystem. See [SQLite's WAL documentation](https://www.sqlite.org/wal.html).

PaperGo can keep its database in PostgreSQL 17 instead of the SQLite file: set `DATABASE_URL` (or `database.url` in `config.yaml`) to a `postgres://` URL whose user may create tables. The first start installs the schema into an empty database. Files, the search index and the integration key stay on local disk beside `DATABASE_PATH`. With PostgreSQL, `GET /v1/storage/status` reports the database and relation sizes and leaves the SQLite journal fields empty, and the `admin` backup and restore commands do not apply; back up the database with PostgreSQL's own tools together with the blob directory and integration key.

For container deployment, use the [Docker guide](docs/docker.md) and [Compose file](deploy/compose.yaml).

Out of the box, `AUTH_MODE=local`: the first start creates `ADMIN_SUBJECT` (default `admin`) with password `admin` in development. Production requires an explicit initial password (`ADMIN_PASSWORD_FILE`, `ADMIN_PASSWORD` or YAML); an omitted password prompts on an interactive first start and fails without a terminal. A non-interactive start with no password and no administrator fails. Later starts do not replace an existing password. HTTP Basic and `POST /auth/login` both sign that account in. For production with an external identity boundary, set `APP_ENV=production` and choose OIDC bearer authentication or trusted proxy authentication. OIDC uses `AUTH_MODE=oidc`, an HTTPS `OIDC_ISSUER`, and the API audience in `OIDC_AUDIENCE`. The provider must issue signed JWT bearer tokens with issuer, audience, subject and expiry claims; locally issued personal access tokens provide scoped credentials for REST integrations. The audience must be registered for this API. Set `OIDC_CLIENT_ID` to a public client registered at the provider so people sign in to the web app there. The [authentik Compose sample](samples/authentik/README.md) runs this mode with authentik, Caddy and HTTPS. The [NPM Compose sample](samples/npm/README.md) uses `AUTH_MODE=proxy`, NPM Access List credentials, a private forwarding peer and a shared secret. Development authentication is rejected in production. Terminate TLS at the ingress and enforce deployment request/rate limits there. The process creates the current schema on an empty data directory at startup. Published binaries upgrade a published database automatically at startup. The Docker image runs as UID 65532 and needs a writable persistent `/data` volume. It includes ONNX Runtime, every preset model and PDFium, so OCR, PDF text reading and local decisions work out of the box ([OCR](docs/ocr.md#configuration)). Semantic and hybrid search are off until `AI_EMBEDDINGS_PROVIDER` names a local model or an OpenAI-compatible endpoint ([search](docs/search.md#semantic-and-hybrid-search)), and AI completions until `AI_CHAT_PROVIDER` names a chat model ([AI completions](docs/ai.md)). CI builds the image and checks startup against a fresh SQLite database.

Back up SQLite with its online backup API or `VACUUM INTO` and back up the referenced blob files consistently; copying only the `.db` file while WAL is active is not a valid backup. Blobs are created before their metadata transaction; ordinary failed writes clean up the object, while a process crash between those steps can leave an unreferenced file. The periodic collector removes unreferenced objects after a one-hour grace period and protects shared file versions and pending/exported packages. Deleted resources retain blobs for recovery. Permanent recycle-bin purge removes retained content and queues storage cleanup durably; failures retry after restart. Audit and workflow run records retain IDs. History retention removes user-visible history entries without deleting snapshots needed by live/publication/event processing.

This is an initial backend foundation, not a completed enterprise certification or deployment. S3 storage, antivirus scanning, quotas, notifications, document text extraction and tracing/metrics export remain future extensions. Benchmarks cover query latency and allocations, writer wait during index builds and WAL growth on configurable collection sizes; concurrent upload throughput, multi-client load and SLOs still need a target workload.

[OCR](docs/ocr.md) reads text in page images with PaddleOCR in ONNX Runtime, for reading text, image optimization and photos to PDF ([documents](docs/documents.md#reading-text)). [AI completions](docs/ai.md) call a chat model immediately or in provider batches.

[Local embeddings](docs/embeddings.md) provides a lazy EmbeddingGemma 2 engine using the shared ONNX Runtime, plus a reproducible Harrier-OSS-v1-0.6B comparison. It can be exercised with `cmd/embed`; application search uses keywords.

The [NPM Compose sample](samples/npm/README.md) provisions an NPM-only authenticated deployment with HTTPS and persistent storage. The [authentik Compose sample](samples/authentik/README.md) provisions an OIDC deployment with authentik for users, groups and MFA, browser sign-in, Caddy for HTTPS, and persistent storage. The [reverse proxy guide](docs/reverse-proxies.md) covers running PaperGo behind Nginx Proxy Manager and other proxies, and syncing identity-provider users and groups.

Multi-node operation requires PostgreSQL runtime integration and shared object storage. See the [storage migration checklist](reports/ent-bob-migration.md) for the current implementation status.

Relevant upstream reference: [Bob](https://bob.stephenafamo.com/).

### Filtered bulk edits

POST `/v1/resources/{collectionID}/bulk-update` queues a durable operation with item.read, item.write and collection write access. Its `query` uses the same typed `QuerySpec` as collection queries, on readable head content; omit it to select all readable head items. A patch may set `content_type_id`, `name`, `keywords` or `values`. For example:

```json
{"query":{"filter":{"field":"status","value":"todo"}},"values":{"status":"done"}}
```

The response is 202 with a `Location` pointing to `/v1/operations/{id}`. Poll that URL for `matched`, `processed`, `updated`, `failed`, `progress` and `failures`. The operation freezes its selection when it starts and edits items in batches of up to 200 per transaction (about a quarter of a second each, so other writes proceed between batches); each item goes through normal permissions, validation, history, uniqueness and event handling under its own savepoint, so one item's failure leaves the others in its batch intact. Values merge; null removes a field. Selection uses head content and does not publish changes. Successful edits survive other items' failures. `succeeded` means execution finished, even when some items failed; `failed` means the operation stopped because collection access, schema or infrastructure failed; earlier successful edits remain committed. Counts include every failure, with at most 50 reported reasons. Results require the starter and current collection read access. Pending work resumes after restart. `/bulk` provides a separate atomic batch of explicit actions.

Lookup fields may target any live collection in the organization, across workspace boundaries. Explicit assignments and defaults validate that every target is a live item of that collection, without requiring target read access or publication. Repeating a stored ID explicitly revalidates it; omitted lookup values survive unrelated edits. A lookup reference does not grant access to the target content.

## Workspace export and import

[Workspace packages](docs/workspace-portability.md) copy a shared workspace to
another PaperGo instance, with its content types, lists, libraries, templates,
views, smart folders, taxonomy, workflows and permissions. Include items and all
file versions, or export configuration only. Workspace settings link to the export
controls; administration provides export, validation and import. Imports create a
new workspace alongside existing workspaces, optionally under a new name, and
remap its internal references atomically. The
[workflow examples](samples/workflows/README.md) and
[batch receipt processing sample](samples/receipts-package/README.md) include ZIPs
that can be installed directly from **Export and import**.

## Administrative backup and restore

Build the administrative CLI with `go build -o papergo-admin ./cmd/admin`:

```sh
papergo-admin backup --file backup.tar.gz
papergo-admin restore --file backup.tar.gz
```

`--database`, `--blobs`, `--search` and `--search-store` select paths and the backend; defaults use `DATABASE_PATH`, `BLOB_PATH`, `SEARCH_STORE` and `SEARCH_DATABASE_PATH` or `SEARCH_ZVEC_PATH`. These commands need filesystem access and do not initialize HTTP authentication. With SQLite search, backup can run while the API is serving: it pauses database writes while snapshotting and copying referenced immutable files; reads continue. The gzip tar archive includes the complete SQLite database (including workflow runs), referenced file bytes and a checksum manifest; it leaves out the SQLite search index. With zvec, stop the API first: backup requires exclusive leases and includes the flushed, closed native directory and its checksums. Existing backup archives are never overwritten. The manifest is limited to 16 MiB.

Stop the API before restoring. Restore rejects active API leases and existing destinations unless `--force` is supplied. It validates the complete archive, current build's schema hash, SQLite integrity/foreign keys, file references, sizes and SHA-256 checksums before replacing the database and blob directory, and restores the archived zvec directory, or removes a missing/SQLite search index for the API to rebuild at startup. Invalid archives leave existing data intact. Installation failures restore the saved destinations. Symlinks, unsafe paths, duplicate entries and appended archives are rejected. Restore accepts archives matching this build's complete schema; it does not perform schema upgrades. [Offline search copy](docs/search.md#copying-between-stores) changes backends without OCR or embedding calls. Persistent `.lease` files coordinate the API and CLI through operating-system locks.

Conditional writes accept quoted numeric ETags, including `W/"n"`. Stale versions return HTTP 412 with a `precondition_failed` problem; duplicate names, keys and cardinality conflicts return 409. Missing required `If-Match` returns 428. Paged REST responses expose `value` and `@odata.nextLink`; query selection and filtering use the native typed request contract.

List settings support [integrations](docs/list-integrations.md), including HTTPS iCalendar subscriptions on ordinary lists, automatic refresh, source-managed fields and recurring calendar events.

Local workflow decisions automatically use the bundled Laya model and shared
ONNX Runtime; explicit `AI_DECISION_MODELS_PATH` and `ONNXRUNTIME_LIB` settings override them. Both `/v1/ai/decisions` and `/v1/ai/taxonomy-decisions` use the verified Laya
multilingual FP32 bundle. See [workflow decisions](docs/decisions.md) for setup,
custom-data routing, managed taxonomy suggestions and guarded human review.

Right-click navigation entries, collection cards, rows or item cards for their available actions. A selected row keeps the selection for workflow, move and delete actions. Move selected entries between folders with `POST /v1/resources/{id}/move-selection`: up to 100 item/folder ids and current versions move together, with no partial changes on failure; nested ancestor/descendant selections are rejected.

Drag entries or an explicit selection onto a folder or breadcrumb to move them. File uploads keep their own drop flow. A move name conflict offers rename-and-retry; a single writable file can replace the destination after confirmation, atomically recycling that destination and moving the source with both versions checked.
