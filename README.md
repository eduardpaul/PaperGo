# PaperGo

Go foundation for headless DMS and CMS applications. Uses **SQLite**, **Ent** for data access, and **Atlas** for reviewed, versioned migrations. One organization per deployment, with multiple workspaces. The API is a modular monolith with immutable content/schema revisions, derived read projections, and local immutable blob storage. [Architecture decisions](docs/architecture.md) explain the extension boundaries. [Application foundation](docs/application-foundation.md) documents rich fields, templates, views, taxonomy, typed relationships, and exclusive permission scopes. [Workflows](docs/workflows.md) are how anything happens after a change or on a schedule: triggers, conditions and flows of activities that people can change, including the built-in processes PaperGo ships, run durably by an embedded runner.

## Run locally

Requires Go 1.26+ (the local toolchain and CI use Go 1.27.1) and Atlas. This workspace has isolated tools under `.tools/`; they are ignored by Git. No Docker or database service is required.

PowerShell, from the repository root:

```powershell
New-Item -ItemType Directory -Force data | Out-Null
.tools/atlas.exe migrate validate --env local
.tools/atlas.exe migrate apply --env local
./scripts/dev.ps1
```

The dev script generates a bearer token, prints it once, and starts on `127.0.0.1:8080`. Supply `-Token` to reuse your own token. `.env.example` documents configuration; the app reads process environment variables and does not automatically load `.env` files.

With globally installed tools, replace `.tools/atlas.exe` with `atlas`; run `go run ./cmd/api` after setting `APP_ENV=development`, `AUTH_MODE=development`, and `DEV_TOKEN` to a random token of at least 32 characters.

```powershell
./scripts/check.ps1
```

Tests use temporary SQLite files and apply the actual versioned migrations, including FTS5, triggers, foreign keys, and Atlas checksum validation. CI also runs the Go race detector and verifies regenerated Ent code.

The machine-readable REST contract is in [`api/openapi.json`](api/openapi.json).

## Data model and requirements

| Requirement | Implementation |
| --- | --- |
| Workspaces, lists, libraries, folders and items | One typed resource hierarchy. A workspace contains lists/libraries; those contain folders/items. Items have one immutable list/library container, including when nested under folders; folders and items move freely within it. Library folders and items carry file names, unique among siblings ignoring case. |
| Delete folders and items | Deleting a folder or item removes it and everything below it from every read, search, query and relationship, and frees its name. The rows remain as final tombstones, so revisions, publications and audit history are retained. |
| WebDAV access to libraries | Optional per library (`webdav_enabled`). Windows Explorer, macOS Finder and Office can browse, open, create, overwrite, rename, move, copy, lock and delete files and folders, with the same permissions, publishing and history as the REST API. |
| Flexible SharePoint-style fields | Per-container typed fields and immutable schema revisions. Types: text/note/email/url/date/datetime, choice, number/integer/decimal, boolean, lookup, term and keywords (by term ID or label); defaults, bounds, length limits, and indexed multi-values. Unknown fields, wrong types and missing required values are rejected. |
| Library as blob plus list item | A library item has the same metadata and permission behavior as a list item, plus immutable blob revisions stored outside SQLite. |
| Unique permissions at every level | SharePoint-style inheritance: only the nearest exclusive scope supplies additive read/read_draft/write/publish/manage grants. Break, copy, or reset inheritance explicitly. Manage implies all actions; write/publish imply draft and published read. Denies are rejected. |
| Typed item relationships | Every link has a workspace relationship type supplying its key, directed/symmetric policy, validated attributes, cardinality limits, and edge ETags. Indexed incoming/outgoing queries require visible, authorized endpoints before pagination. Cross-workspace links are rejected. |
| Publish any list/library item | Publishing disabled by default: new items, edits and uploads are automatically published. With publishing enabled, changes create drafts and explicit publish/unpublish controls the published pointer. Ordinary readers see published revisions only. Library revisions pin their blob. |
| Preserve content history | Every item create, metadata edit and upload stores an immutable revision tied to its schema revision. Head and published surfaces are independent. Lifecycle/ACL changes advance the lock version without creating content revisions. |
| Tags on all content | Built-in tags on every resource, with a normalized indexed projection maintained by database triggers. The workspace tag vocabulary lists tags in use with counts of what the caller can read. |
| Read-heavy browsing and search | Keyset pagination and permission filtering before LIMIT; FTS5 and tags on the selected head/published surface; optional typed field indexes for exact filters and ranges. Multi-step reads use a consistent database snapshot. |
| Enterprise backend foundation | Fail-closed bearer authentication, production OIDC, transactional audit events, optimistic concurrency, strict JSON, upload limits, structured logs, health probes, graceful shutdown and non-root container build. |
| Reusable application schemas | Shared field catalogs, multiple collection content types, cross-field rules, active-field removal and templates with version-checked adoption; immutable effective schemas. |
| Business keys | Indexed scalar uniqueness across head and published values, enforced transactionally for single and bulk writes. |
| Saved views and controlled taxonomy | Bounded typed queries, sorting, pagination, opt-in authorized totals, grouping; a governed taxonomy of term groups, sets and hierarchical terms (labels, synonyms, colors, order, deprecation, move, merge, SharePoint CSV import, portable packages) and keywords people add, which managers promote into managed sets. Workflows can react to terms gained or lost. |
| Smart folders | Private or shared live queries across collections, descendant-term matching, metadata navigation, UTC-relative filters and physical folder inclusion. See [smart folders](docs/smart-folders.md). |
| Customizable automation | Workflows react to item changes, schedules, manual starts and each other's events, with conditions in the query filter language and flows of activity nodes. Versions are immutable; runs are durable, exactly once per step, act with their author's permissions, and start from a durable event log written with the change that triggers them, so any server can run them. Built-in workflows ship core processes that people configure, turn off or copy. See [workflows](docs/workflows.md). |

## API

Every `/v1` request requires `Authorization: Bearer <token>`. Health endpoints are public. IDs are UUID strings. Lists of resources and relationships return `{ "data": [...], "next_cursor": "..." }`; pass `after` to continue. Default page size is 50, maximum 100.

| Method | Route | Purpose |
| --- | --- | --- |
| POST / GET | `/v1/workspaces` | Create a workspace / list accessible workspaces |
| GET / PATCH / DELETE | `/v1/resources/{id}` | Read / update names, tags, item values, `parent_id` (move) and collection settings / delete a folder or item |
| POST / GET | `/v1/resources/{id}/children` | Create / browse children |
| POST | `/v1/resources/{id}/bulk` | Atomically create, update/move, publish, unpublish or delete up to 100 items in one list/library |
| GET | `/v1/workspaces/{id}/tags` | The tag vocabulary with counts of readable resources; `collection_id`, `prefix`, `after`, `limit` |
| POST / GET | `/v1/smart-folders` | Create / browse personal and shared query definitions |
| GET / PUT / DELETE | `/v1/smart-folders/{id}` | Read / replace / delete a definition with its own ETag |
| POST | `/v1/smart-folders/{id}/query`, `/query/groups` | Query live membership / navigate metadata groups with authorized counts |
| POST | `/v1/smart-folders/{id}/items` | Classify an existing item or create an item matching the definition/path |
| DELETE | `/v1/smart-folders/{id}/items/{itemID}` | Remove classification while preserving the item and location |
| GET / POST | `/v1/workspaces/{id}/smart-folders/export`, `/import` | Export / atomically merge portable shared definitions |
| GET | `/v1/resources?workspace_id=...&q=...&tag=...` | Workspace search and tag filtering; optional `parent_id` narrows to direct children |
| POST / GET | `/v1/resources/{id}/fields` | Add / read list or library field definitions |
| PATCH | `/v1/resources/{id}/fields/{fieldID}` | Evolve label, choices, required or indexed; uses the container ETag |
| GET | `/v1/resources/{id}/schemas` | Manager-only schema history; paginate with `after_revision` |
| PUT / GET | `/v1/resources/{id}/permissions` | Replace / read explicit ACLs and inheritance setting |
| POST / GET | `/v1/items/{id}/relationships` | Create / query named edges; filter by `direction=incoming` or `outgoing` and `name` |
| DELETE | `/v1/items/{id}/relationships/{linkID}` | Remove an outgoing relationship |
| POST / GET | `/v1/items/{id}/publications` | Publish / read immutable snapshots; paginate with `after_version` |
| POST | `/v1/items/{id}/unpublish` | Remove the published surface while retaining history |
| GET | `/v1/items/{id}/revisions` | Draft-reader content history; paginate with `after_revision` |
| GET | `/v1/items/{id}/schema?surface=auto` | Immutable schema of the caller's visible content revision |
| PUT / GET | `/v1/items/{id}/content` | Upload a blob / download the selected revision's blob; draft readers may request historical `blob_id` |
| GET | `/v1/workspaces/{id}/audit` | Workspace manager audit feed |
| POST / GET | `/v1/webdav-credentials` | Issue / list the caller's WebDAV app passwords |
| DELETE | `/v1/webdav-credentials/{id}` | Revoke one of the caller's WebDAV app passwords |
| WebDAV | `/webdav/{libraryID}/...` | A WebDAV-enabled library as a network drive; see [WebDAV](#webdav) |
| GET | `/health/live`, `/health/ready` | Process liveness / database and migration readiness |

Read/update responses carry a quoted numeric `ETag`. PATCH, resource DELETE, catalog PUT, view/relationship DELETE, template application, ACL replacement, blob upload, publish and unpublish require `If-Match: "<current version>"`. Missing preconditions return 428; stale versions return 409. These mutations consume a lock version; publishing an already published head returns 409. Content `revision_number`, blob `version`, and resource lock `version` are independent counters. Upload returns `resource_version` and an ETag for the resource. Publication event `version` records the lock version consumed by the transition. PATCH `values` replaces the whole custom-value object.

GET resources and browsing support `surface=auto|head|published`. Auto chooses head for draft readers and published for ordinary readers. An unpublished item returns 404 to a reader; explicit head requests require draft access. Names, tags, values, search matches, field filters and downloads all use the chosen surface. Readers cannot download draft or arbitrary historical blobs. A published resource's lock version may advance when editors save drafts; its `updated_at` describes the selected content revision.

Fields use stable immutable keys, types, decimal scales, multiplicity, and reference scopes. Labels, choices, required and indexed flags can evolve; existing revisions retain their original schema. Adding a required field to a populated collection requires a valid default; setting an existing field required affects future edits. Set `indexed:true` to enable typed queries. Changing this flag rebuilds both surfaces atomically. For example, `GET /v1/resources/{listID}/children?filter_field=amount&filter_op=gte&filter_value=125.00`. Operators: `eq`, `gt`, `gte`, `lt`, `lte`; boolean supports only `eq`. Filters require a list/library/folder scope and apply to direct children.

Read an item's `/schema` for the schema paired with its selected revision. Revision history includes each immutable schema in `edges.schema_revision`, allowing draft readers to interpret historical values without collection management access. The collection `/fields` endpoint describes current configuration.

`integer` accepts exact JSON integers within signed 64 bits. `decimal` accepts a decimal string or exact JSON number, with fixed `scale` from 0 to 9; it stores a canonical string in content and a signed 64-bit scaled integer in indexes. Excess fractional digits and overflow are rejected without rounding. Use strings for decimals and an exact-number parser in clients; JavaScript clients should avoid unsafe numeric literals above 2^53. `number` is an approximate floating-point query type. Optional null values produce no field-index row.

Example request bodies, in order:

```json
{"name":"Engineering","tags":["internal"]}
```

POST the workspace ID's `/children` to create a library:

```json
{"kind":"library","name":"Contracts","tags":["legal"],"publishing_enabled":true}
```

POST the library's `/fields` (requires manage):

```json
{"key":"status","label":"Status","type":"choice","required":true,"choices":["draft","approved"],"indexed":true}
```

POST the library's `/children`:

```json
{"kind":"item","name":"Supplier agreement","values":{"status":"draft"},"tags":["supplier"]}
```

Upload bytes to the item's `/content` with `Content-Type: application/pdf`, `X-Filename: agreement.pdf`, and `If-Match: "1"`. Upload returns blob metadata and the new item ETag; GET supports Range requests and always sends an attachment disposition. Publish with POST `/publications` and the new `If-Match` value.

When publishing is disabled, library metadata is visible immediately, including before the first upload; downloading returns 404 until a blob exists. Explicit publication in an enabled library requires a blob. Enabling publishing keeps existing published content and makes future changes drafts. Disabling it requires manage access and publishes all current heads atomically, advancing affected item lock versions.

Break item permission inheritance using PUT `/permissions` and its current `If-Match`:

```json
{"inherit":false,"grants":[{"subject":"local-admin","action":"manage","effect":"allow"},{"subject":"reviewer-subject","action":"read","effect":"allow"}]}
```

Reset with `{"inherit":true,"grants":[]}`, or break and copy with `{"inherit":false,"copy_inherited":true}`. Inherited resources cannot carry local grants. GET permissions identifies the effective scope and grants. An ACL replacement that removes the caller's manage access is rolled back. A workspace creator receives manage access atomically. Every authenticated principal can create its own workspace. Subject values are exact OIDC `sub` claims; groups and organization provisioning are not implemented.

Create a relationship type in the workspace (`POST /v1/workspaces/{id}/relationship-types`), then link items with it:

```json
{"type_id":"<relationship-type-id>","target_id":"<another-item-id>","metadata":{}}
```

The link takes its name, direction, attribute schema and cardinality from the type.

ACL and publication visibility filtering happen before LIMIT, so hidden rows neither shorten pages nor supply cursors. Search treats input as a literal FTS phrase, uses Unicode tokenization, and returns stable ID order, not relevance order. FTS indexes metadata text but does not extract PDF/Office document contents. Browsing a scope requires read access to that scope; explicitly granted items remain accessible directly by ID. Relationships are live, separately audited edges; publication does not freeze a relationship graph.

Move a folder or item with PATCH `{"parent_id":"<folder or collection id>"}`: the target must be in the same list or library, not below the moved folder, and writable by the caller. A move consumes a lock version but creates no content revision; inheriting resources take the new location's permission scope. DELETE `/v1/resources/{id}` with `If-Match` deletes a folder or item and everything below it; it requires write access to all of it, removes relationships to the deleted items, and records one `resource.delete` audit event. Workspaces, lists and libraries cannot be deleted. Deletion is final, but the tombstones keep every revision, publication and audit event.

## WebDAV

A library created with `"webdav_enabled": true`, or updated to it by a manager, is also served at `https://<host>/webdav/<libraryID>/` (RFC 4918 class 1 and 2). Its folders are collections and its items are files whose bytes are the caller's visible revision: head for draft readers, published for ordinary readers. Every request runs through the same service as the REST API, so permissions, publishing, validation, revisions and audit events are identical. Libraries without the flag and paths the caller cannot read return 404.

WebDAV accepts the API bearer token or HTTP Basic. Windows Explorer and macOS Finder only use Basic, so each user issues an app password with `POST /v1/webdav-credentials` and `{"label":"Laptop"}` (optionally `expires_at`). The response shows `password` once; sign in with any user name and that password. Only its SHA-256 is stored, each principal can hold 20, and `DELETE /v1/webdav-credentials/{id}` revokes one.

To connect from Windows, choose **Map network drive** in Explorer and enter `https://<host>/webdav/<libraryID>/`, or run `net use Z: https://<host>/webdav/<libraryID>/ /user:me <password>`. Windows sends Basic credentials only over HTTPS unless `BasicAuthLevel` is set to `2` under `HKLM\SYSTEM\CurrentControlSet\Services\WebClient\Parameters`, which local HTTP development needs (restart the WebClient service afterwards). The Windows client also rejects files above its `FileSizeLimitInBytes` (50 MB by default); the server limit is `MAX_UPLOAD_BYTES`.

| WebDAV | Effect |
| --- | --- |
| PROPFIND | Depth `0` or `1`; `infinity` is refused with `propfind-finite-depth`. A folder lists at most 10,000 entries. |
| GET / HEAD | Download the visible content, with Range and conditional requests. |
| PUT | Create an item whose first revision holds the content, or add a content revision to an existing file. `If-Match` and `If-None-Match` are honored. |
| MKCOL | Create a folder. |
| MOVE | Rename or move within the library in one transaction, replacing the destination unless `Overwrite: F`. Destinations in other libraries return 502, so clients copy and delete instead. |
| COPY | Create new items carrying the source's bytes, tags and field values; copying onto an existing file adds a content revision. COPY and MOVE refuse (403) a destination that is the source itself, contains it, or lies inside it. |
| DELETE | Delete the folder or file and everything below it, as the REST API does. |
| LOCK / UNLOCK | Exclusive or shared write locks of at most one hour, refreshable, held in memory and released on restart. |
| PROPPATCH | Live DAV properties are refused; others, such as Windows timestamps, are acknowledged but not stored. |

New files take the library's field defaults; a library with a required field that has no default refuses new files with 403 and the reason. In a library with publishing enabled, writes are drafts: readers keep the published names and bytes until an editor publishes. File names follow the library rules above, and each saved version (Windows writes an empty file before its content) becomes a content revision.

## Schema changes

PaperGo is unreleased, so the whole database schema lives in one file, `migrations/20261007000100_schema.sql`, which is edited in place. Do not add migration files, backfills, upgrade paths or compatibility code (see [AGENTS.md](AGENTS.md)). Local databases are disposable: delete `data/` and apply the schema again.

`ent/schema/` defines entity fields and generated access code. The schema file also defines composite ownership foreign keys and SQLite-specific search, tags, type checks, immutable-history and permission-scope triggers, which an Ent-generated table diff does not express.

```powershell
go generate ./ent
# Edit migrations/20261007000100_schema.sql to match ent/schema, then:
atlas migrate hash --dir file://migrations
Remove-Item -Recurse -Force data; New-Item -ItemType Directory data | Out-Null
atlas migrate apply --env local
```

`go run ./cmd/schema-diff -dev-db <disposable db>` prints the SQL Ent would need, which helps when editing the schema file; do not commit its output as a new migration. The API never auto-migrates and refuses to start unless the schema revision in `internal/database/database.go` is applied.

## Deployment and scaling

Use one API process with a local persistent disk. SQLite runs WAL mode, foreign-key enforcement and FULL synchronous durability. Write transactions take the write lock when they begin and wait up to 30 seconds for it, so API requests and workflow steps share the single writer; an in-process mutex keeps request writes in order, and WebDAV locks live in the same process; readers use a bounded connection pool. The workflow runner runs inside the API process and keeps its tables in the same database file; `NODE_ID` names the process (default `local`) and `RUN_RETENTION` sets how long finished runs are kept (default `720h`). Do not run multiple API replicas against a shared network filesystem. See [SQLite's WAL documentation](https://www.sqlite.org/wal.html).

For production, set `APP_ENV=production`, `AUTH_MODE=oidc`, an HTTPS `OIDC_ISSUER`, and the API audience in `OIDC_AUDIENCE`. The provider must issue signed JWT bearer tokens with issuer, audience, subject and expiry claims; opaque tokens require a separate introspection adapter. The audience must be registered for this API. Development authentication is rejected in production. Terminate TLS at the ingress, enforce deployment request/rate limits there, and apply migrations before starting the API. The Docker image runs as UID 65532 and needs a writable persistent `/data` volume. Docker is not installed in the current workspace, so image verification requires CI or another host.

Back up SQLite with its online backup API or `VACUUM INTO` and back up the referenced blob files consistently; copying only the `.db` file while WAL is active is not a valid backup. Blobs are created before their metadata transaction; ordinary failed writes clean up the object, while a process crash between those steps can leave an unreferenced file. A deployment should reconcile such orphan files after a grace period. Deleted resources keep their blobs, because their revisions are retained; purging tombstones and pruning old revisions need explicit retention rules first.

This is an initial backend foundation, not a completed enterprise certification or deployment. Group ACLs, S3 storage, antivirus scanning, quotas, approval and task activities, notifications, idempotency keys, document text extraction, tracing/metrics export and restore tooling remain future extensions. Performance has functional coverage; representative load benchmarks and SLOs still need a target workload.

The [runner plan](docs/plans/runner.md) describes the embedded runner and the next workflow phases; notifications and tasks build on workflows as activities and built-in workflows.

To scale later, retain the service and API boundaries, introduce a PostgreSQL connection adapter, regenerate/review database-specific Atlas migrations, replace FTS5 and tag SQL, and use a shared object-store implementation of the storage port. A database migration is required; changing the connection string alone is insufficient.

Relevant upstream references: [Ent versioned migrations](https://entgo.io/docs/versioned-migrations/), [Atlas](https://atlasgo.io/getting-started), [SQLite FTS5](https://www.sqlite.org/fts5.html).
