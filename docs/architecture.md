# DMS / CMS foundation

## Deployment boundary

One organization owns one deployment, SQLite database, and blob directory. A workspace is an organizational content scope within that deployment. No tenant ID is inferred from an HTTP header. OIDC subjects are stable principal identifiers; authorization happens in the service and SQL read predicates. Multi-organization SaaS, group membership and cross-deployment identities require explicit future design.

## Authoritative content and derived reads

`resources` holds immutable identity and container membership, plus hierarchy, permissions inheritance, lock version, and revision pointers. An item belongs to exactly one list or library, including when nested in a folder; folders and items move only within that collection, never below themselves, which triggers enforce. The resource's name/tags/values cache represents head content; it is never used as a reader's published representation. In libraries, the case-folded head name of each live folder and item (`name_key`) is unique per parent, so the library can be presented as a file system.

Deleting a folder or item sets `deleted_at` on it and its live descendants in one transaction. Tombstones are final and keep their rows, so append-only revisions, publications and audit events keep valid references. Deletion removes the derived surfaces and field indexes, which hides deleted items from search, queries and relationship visibility; it also deletes live relationships, and `authorize` treats a tombstone as missing.

`schema_revisions` freezes the collection's shared field catalog and content types, including each type's field membership and cross-field rules. Field keys, types and decimal scales are stable identities; labels and validation options evolve through new schema revisions. Items have immutable collection-owned content type assignments. `item_revisions` stores complete name, tags, exact JSON payload, content type and schema references, and optional blob reference. Revisions are append-only and database triggers prevent update/delete. Publishing selects an existing revision and never revalidates old content against a newer schema.

The item schema endpoint follows the same visibility rules as content and returns its paired immutable schema. Draft-reader revision history includes schema snapshots, so consumers can interpret historical field labels and options without access to collection management.

`item_surfaces` projects the head and published revisions. FTS5, normalized tag tables, and typed `field_values` are derived from these surfaces. A reader with ordinary read permission uses published; draft readers/editors use head by default. Index configuration can change independently of the immutable content. Projections retain historical field types/scales and use current index flags only when compatible. Index changes are tracked `operations` that rebuild one field's rows a batch per write transaction, never holding the single SQLite writer for a whole collection; a field is queryable only once its `index_status` is `ready`. Optional null values have no index row.

Indexes are opt-in to bound write amplification. Typed equality/range queries are scoped to a collection and fixed operators; neither arbitrary SQL nor JSON paths are accepted. Integer values and scaled decimal units use signed 64-bit storage. Decimal content is represented as a canonical string, so its precision survives HTTP serialization and history reads.

Smart folders store private or shared query definitions separately from resources. A bounded collection scan and batched catalogs feed SQL UNION branches over selected content surfaces; a recursive taxonomy CTE matches descendants through indexed term parents and field values. Navigation groups and authorized counts use the same SQL pipeline. Packages resolve target taxonomy leaves and ancestors in batches, and import/classification writes use the transaction and item validation pipeline. See [smart folders](smart-folders.md) for ownership, limits and routes.

## Transactions and concurrency

One service instance serializes writes for the SQLite deployment. Authorization, lock checks, revision allocation, pointers, projections, publication events and audit entries commit together. Failed writes roll back all database effects. `If-Match` uses a resource lock version; content revision numbers and blob revision numbers have separate lifecycles. Publish/unpublish and permission changes consume lock versions without creating content revisions.

Reads bind authorization, publication visibility, page selection and hydration to one SQLite snapshot. Authorization uses only the nearest exclusive permission scope. New resources inherit; local grants require breaking inheritance. Grants within a scope are additive, and resetting inheritance removes local grants. Break/copy/reset changes, and moves of inheriting resources, affect inherited descendants immediately while nested exclusive scopes remain independent. SQL predicates filter both resource and relationship endpoints before LIMIT. This avoids hidden candidates producing empty pages or revealing IDs through cursors. The recursive hierarchy depth is bounded at 32.

The chosen publishing policy belongs to the collection. Automatic mode immediately publishes each content change. Explicit mode retains the previous published revision while editors change head. Unpublish removes the published projection, retaining immutable content and lifecycle events. Policy changes require manage access; switching to automatic mode publishes existing heads in one transaction.

Blob bytes are written through the storage port before the database transaction. Metadata pins immutable object keys; failures clean up newly staged objects. A crash between object persistence and metadata commit can leave an orphan, so production reconciliation needs a grace period. This is the only cross-system transaction gap in the current local storage path.

## WebDAV

`internal/webdav` presents each WebDAV-enabled library as a file system at `/webdav/{libraryID}/`. It is a protocol adapter only: the `dms` library-file operations resolve paths, authorize, and commit each PUT, MKCOL, MOVE and DELETE as one transaction, reusing the same create, update, blob-attach and delete code as REST. A new file's first revision already holds its blob. MOVE with overwrite deletes the destination and moves the source atomically. COPY reads the source and writes new entries, so a recursive copy is not one transaction.

Paths resolve by the caller's visible names, using head names for draft readers and published names for others. Head names are unique per folder, but an unpublished rename can leave a reader seeing a published name that equals a sibling's. Then the owner of that head name wins, else the lowest ID, and listings and path resolution apply the same rule. Clients authenticate with API bearer tokens or Basic app passwords (`webdav_credentials`, stored as SHA-256). Write locks live in process memory, which matches the single-writer deployment; a shared lock store must precede multiple replicas.

## Relationships and extensions

Typed relationships connect items within one workspace; every link references a directed or symmetric relationship type. Types define validated scalar attributes and optional directional cardinality; edge mutations use their own concurrency version. They are live edges with their own audit events. Both endpoints must be readable and have a visible content surface to appear in a query. Publishing an item does not publish its linked target or freeze its relationship graph. A future release requiring reproducible relationship graphs should add immutable relationship revisions and published-edge projections explicitly.

Keep HTTP decoding/authentication in `internal/httpapi`, the WebDAV protocol in `internal/webdav`, business invariants in `internal/dms`, database initialization/migration checks in `internal/database`, and object persistence behind `internal/storage.Store`. Ent is the current persistence implementation, not an API contract for consumers. Application-specific workflows can build on collection schemas and typed relationships without changing the core item identity.

When scale warrants PostgreSQL, replace SQLite-specific authorization/search adapters and review Atlas migrations for native indexes, composite constraints and row-level security. Add a shared object store before introducing multiple API replicas. Group expansion, background projection workers and external search should follow measured workloads; any asynchronous projection must retain authorization checks at query time and define its consistency contract.

## Schema and verification

The complete schema is one Atlas migration file, edited in place until the first release; there are no upgrade paths or compatibility layers (see [AGENTS.md](../AGENTS.md)). Each resource stores its nearest exclusive permission scope (`scope_id`), maintained by triggers, so authorization is an indexed lookup rather than a hierarchy walk.

Tests apply that schema file. Coverage includes ownership and move constraints, permission-scope maintenance, deletion tombstones, WebDAV protocol behavior and locking, exact values through REST, head/published search and blobs, typed index rebuilds, schema/history immutability, authorized pagination, and concurrent optimistic updates. Concurrent-client load testing, restoration exercises, group provisioning, workflow approval, quotas, malware scanning, idempotency keys and observability exports remain deployment/application work.

## Application configuration

Workspace-owned schema templates, taxonomy, and relationship types use workspace authorization. Collection-owned views and content types use collection authorization. Applying a template explicitly adopts its checked fields and rules into a selected content type and one immutable effective schema revision; existing content is never rewritten. Rich field options and defaults are retained in those snapshots. Multiplicity and reference scope are field identity, with ordered multi-value indexes for each visible surface. Active-field deletion removes catalog membership and derived indexes while keeping frozen history.

`business_keys` reserves normalized scalar values from both live surfaces with a unique `(container_id,field_key,value)` constraint. Claims are replaced within the same transaction as revision/surface writes and removed with unpublication or deletion. Administrative uniqueness changes rebuild claims in bounded batches and roll back on collisions. Collections without unique fields skip claim work on content writes.

The bounded query specification compiles typed predicates, visible-surface sort keys, opt-in totals, and group counts into parameterized SQL. Collection query pages are driven by the sort column's index (`field_values` for custom fields, `item_surfaces` for system fields) with row-value keyset seeks; `auto` merges an ordered head scan for draft readers with an ordered published scan for everyone else. Authorization happens before aggregation and pagination. Query selection and hydration share a snapshot; cursors bind caller, schema, collection, surface, and query. Saved views grant no access. [API behavior and examples](application-foundation.md) describe these contracts.
