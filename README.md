# PaperGo

Go foundation for headless DMS and CMS applications. Uses **SQLite**, **Ent** for data access, and **Atlas** for reviewed, versioned migrations. One organization per deployment, with multiple workspaces. The API is a modular monolith with immutable content/schema revisions, derived read projections, and local immutable blob storage. [Architecture decisions](docs/architecture.md) explain the extension boundaries. [Application foundation](docs/application-foundation.md) documents rich fields, templates, views, taxonomy, typed relationships, and exclusive permission scopes.

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
| Workspaces, lists, libraries, folders and items | One typed resource hierarchy. A workspace contains lists/libraries; those contain folders/items. Items have one immutable list/library container, including when nested under folders. |
| Flexible SharePoint-style fields | Per-container typed fields and immutable schema revisions. Types: text/note/email/url/date/datetime, choice, number/integer/decimal, boolean, lookup and term; defaults, bounds, length limits, and indexed multi-values. Unknown fields, wrong types and missing required values are rejected. |
| Library as blob plus list item | A library item has the same metadata and permission behavior as a list item, plus immutable blob revisions stored outside SQLite. |
| Unique permissions at every level | SharePoint-style inheritance: only the nearest exclusive scope supplies additive read/read_draft/write/publish/manage grants. Break, copy, or reset inheritance explicitly. Manage implies all actions; write/publish imply draft and published read. Denies are rejected. |
| Named directional item relationships | Legacy named links plus typed directed/symmetric policies, validated attributes, cardinality limits, and edge ETags. Indexed incoming/outgoing queries require visible, authorized endpoints before pagination. Cross-workspace links are rejected. |
| Publish any list/library item | Publishing disabled by default: new items, edits and uploads are automatically published. With publishing enabled, changes create drafts and explicit publish/unpublish controls the published pointer. Ordinary readers see published revisions only. Library revisions pin their blob. |
| Preserve content history | Every item create, metadata edit and upload stores an immutable revision tied to its schema revision. Head and published surfaces are independent. Lifecycle/ACL changes advance the lock version without creating content revisions. |
| Tags on all content | Built-in tags on every resource, with a normalized indexed projection maintained by database triggers. |
| Read-heavy browsing and search | Keyset pagination and permission filtering before LIMIT; FTS5 and tags on the selected head/published surface; optional typed field indexes for exact filters and ranges. Multi-step reads use a consistent database snapshot. |
| Enterprise backend foundation | Fail-closed bearer authentication, production OIDC, transactional audit events, optimistic concurrency, strict JSON, upload limits, structured logs, health probes, graceful shutdown and non-root container build. |
| Reusable application schemas | Workspace templates with explicit, version-checked collection adoption; immutable effective schemas. |
| Saved views and controlled taxonomy | Bounded typed queries, sorting, pagination, authorized totals/grouping; stable hierarchical terms with localized labels, synonyms, and deprecation. |

## API

Every `/v1` request requires `Authorization: Bearer <token>`. Health endpoints are public. IDs are UUID strings. Lists of resources and relationships return `{ "data": [...], "next_cursor": "..." }`; pass `after` to continue. Default page size is 50, maximum 100.

| Method | Route | Purpose |
| --- | --- | --- |
| POST / GET | `/v1/workspaces` | Create a workspace / list accessible workspaces |
| GET / PATCH | `/v1/resources/{id}` | Read / update names, tags and item values |
| POST / GET | `/v1/resources/{id}/children` | Create / browse children |
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
| GET | `/health/live`, `/health/ready` | Process liveness / database and migration readiness |

Read/update responses carry a quoted numeric `ETag`. PATCH, catalog PUT, view/relationship DELETE, template application, ACL replacement, blob upload, publish and unpublish require `If-Match: "<current version>"`. Missing preconditions return 428; stale versions return 409. These mutations consume a lock version; publishing an already published head returns 409. Content `revision_number`, blob `version`, and resource lock `version` are independent counters. Upload returns `resource_version` and an ETag for the resource. Publication event `version` records the lock version consumed by the transition. PATCH `values` replaces the whole custom-value object.

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

Create an outgoing relationship:

```json
{"target_id":"<another-item-id>","name":"references","inverse_name":"referenced_by","metadata":{"purpose":"supporting_document"}}
```

ACL and publication visibility filtering happen before LIMIT, so hidden rows neither shorten pages nor supply cursors. Search treats input as a literal FTS phrase, uses Unicode tokenization, and returns stable ID order, not relevance order. FTS indexes metadata text but does not extract PDF/Office document contents. Browsing a scope requires read access to that scope; explicitly granted items remain accessible directly by ID. Relationships are live, separately audited edges; publication does not freeze a relationship graph.

## Migrations and schema changes

`ent/schema/` defines entity fields and generated access code. Versioned migrations also define composite ownership foreign keys and SQLite-specific search, tags, type checks and immutable-history triggers. Preserve these constraints when evolving the schema; an Ent-generated table diff alone does not express the complete database contract.

Migration `20261007000400` freezes existing field definitions, retains saved publication snapshots as revisions, and appends each current draft. Previously overwritten drafts cannot be recovered. Existing collections keep explicit publishing enabled to preserve their behavior; new collections default to automatic publishing. The migration aborts atomically if legacy deny grants exist. Review those ACLs and replace them with additive grants and inheritance boundaries before applying it; the API never silently converts denies into allows.

```powershell
go generate ./ent
# Use a disposable dev database with the EXISTING migrations applied first.
atlas migrate apply --dir file://migrations --url 'sqlite://data/schema-dev.db?_fk=1'
go run ./cmd/schema-diff -name add_field -dev-db data/schema-dev.db
atlas migrate validate --env local
# Review the SQL before applying it to a deployment database.
atlas migrate apply --env local
```

`schema-diff` compares Ent tables against the supplied database. It does not automatically rewrite the custom FTS/tag tables or triggers. Inspect generated SQL for accidental removal of these objects, adjust it, then run `atlas migrate hash --dir file://migrations`. Update the expected migration version in `internal/database/database.go` whenever the deployment schema changes. The API never auto-migrates and refuses to start on an incompatible revision.

## Deployment and scaling

Use one API process with a local persistent disk. SQLite runs WAL mode, foreign-key enforcement, a 5-second busy timeout and FULL synchronous durability. An in-process mutex serializes application writes; readers use a bounded connection pool. Do not run multiple API replicas against a shared network filesystem. See [SQLite's WAL documentation](https://www.sqlite.org/wal.html).

For production, set `APP_ENV=production`, `AUTH_MODE=oidc`, an HTTPS `OIDC_ISSUER`, and the API audience in `OIDC_AUDIENCE`. The provider must issue signed JWT bearer tokens with issuer, audience, subject and expiry claims; opaque tokens require a separate introspection adapter. The audience must be registered for this API. Development authentication is rejected in production. Terminate TLS at the ingress, enforce deployment request/rate limits there, and apply migrations before starting the API. The Docker image runs as UID 65532 and needs a writable persistent `/data` volume. Docker is not installed in the current workspace, so image verification requires CI or another host.

Back up SQLite with its online backup API or `VACUUM INTO` and back up the referenced blob files consistently; copying only the `.db` file while WAL is active is not a valid backup. Blobs are created before their metadata transaction; ordinary failed writes clean up the object, while a process crash between those steps can leave an unreferenced file. A deployment should reconcile such orphan files after a grace period. Publication/blob revision retention and resource deletion policies need to be defined before adding destructive endpoints.

This is an initial backend foundation, not a completed enterprise certification or deployment. Group ACLs, S3 storage, antivirus scanning, quotas, workflow approval, idempotency keys, document text extraction, tracing/metrics export and restore tooling remain future extensions. Performance has functional coverage; representative load benchmarks and SLOs still need a target workload.

To scale later, retain the service and API boundaries, introduce a PostgreSQL connection adapter, regenerate/review database-specific Atlas migrations, replace FTS5 and tag SQL, and use a shared object-store implementation of the storage port. A database migration is required; changing the connection string alone is insufficient.

Relevant upstream references: [Ent versioned migrations](https://entgo.io/docs/versioned-migrations/), [Atlas](https://atlasgo.io/getting-started), [SQLite FTS5](https://www.sqlite.org/fts5.html).
