# DMS / CMS foundation

## Deployment boundary

One organization owns one deployment, SQLite database, and blob directory. A workspace is an organizational content scope within that deployment. No tenant ID is inferred from an HTTP header. OIDC subjects are stable principal identifiers; authorization happens in the service and SQL read predicates. Multi-organization SaaS, group membership and cross-deployment identities require explicit future design.

## Authoritative content and derived reads

`resources` holds immutable identity, hierarchy, container membership, permissions inheritance, lock version, and revision pointers. An item belongs to exactly one list or library, including when nested in a folder. The resource's name/tags/values cache represents head content; it is never used as a reader's published representation.

`schema_revisions` freezes the collection's field definitions. Field keys, types and decimal scales are stable identities; labels and validation options evolve through new schema revisions. `item_revisions` stores complete name, tags, exact JSON payload, schema reference, and optional blob reference. Revisions are append-only and database triggers prevent update/delete. Publishing selects an existing revision and never revalidates old content against a newer schema.

The item schema endpoint follows the same visibility rules as content and returns its paired immutable schema. Draft-reader revision history includes schema snapshots, so consumers can interpret historical field labels and options without access to collection management.

`item_surfaces` projects the head and published revisions. FTS5, normalized tag tables, and typed `field_values` are derived from these surfaces. A reader with ordinary read permission uses published; draft readers/editors use head by default. Index configuration can change independently of the immutable content. Projections retain historical field types/scales and use current index flags only when compatible. Optional null values have no index row.

Indexes are opt-in to bound write amplification. Typed equality/range queries are scoped to a collection and fixed operators; neither arbitrary SQL nor JSON paths are accepted. Integer values and scaled decimal units use signed 64-bit storage. Decimal content is represented as a canonical string, so its precision survives HTTP serialization and history reads.

## Transactions and concurrency

One service instance serializes writes for the SQLite deployment. Authorization, lock checks, revision allocation, pointers, projections, publication events and audit entries commit together. Failed writes roll back all database effects. `If-Match` uses a resource lock version; content revision numbers and blob revision numbers have separate lifecycles. Publish/unpublish and permission changes consume lock versions without creating content revisions.

Reads bind authorization, publication visibility, page selection and hydration to one SQLite snapshot. Authorization uses only the nearest exclusive permission scope. New resources inherit; local grants require breaking inheritance. Grants within a scope are additive, and resetting inheritance removes local grants. Break/copy/reset changes affect inherited descendants immediately while nested exclusive scopes remain independent. SQL predicates filter both resource and relationship endpoints before LIMIT. This avoids hidden candidates producing empty pages or revealing IDs through cursors. The recursive hierarchy depth is bounded at 32.

The chosen publishing policy belongs to the collection. Automatic mode immediately publishes each content change. Explicit mode retains the previous published revision while editors change head. Unpublish removes the published projection, retaining immutable content and lifecycle events. Policy changes require manage access; switching to automatic mode publishes existing heads in one transaction.

Blob bytes are written through the storage port before the database transaction. Metadata pins immutable object keys; failures clean up newly staged objects. A crash between object persistence and metadata commit can leave an orphan, so production reconciliation needs a grace period. This is the only cross-system transaction gap in the current local storage path.

## Relationships and extensions

Named directional and typed directed/symmetric relationships connect items within one workspace. Typed policies define validated scalar attributes and optional directional cardinality; edge mutations use their own concurrency version. They are live edges with their own audit events. Both endpoints must be readable and have a visible content surface to appear in a query. Publishing an item does not publish its linked target or freeze its relationship graph. A future release requiring reproducible relationship graphs should add immutable relationship revisions and published-edge projections explicitly.

Keep HTTP decoding/authentication in `internal/httpapi`, business invariants in `internal/dms`, database initialization/migration checks in `internal/database`, and object persistence behind `internal/storage.Store`. Ent is the current persistence implementation, not an API contract for consumers. Application-specific workflows can build on collection schemas and named links without changing the core item identity.

When scale warrants PostgreSQL, replace SQLite-specific authorization/search adapters and review Atlas migrations for native indexes, composite constraints and row-level security. Add a shared object store before introducing multiple API replicas. Materialized permission scopes, group expansion, background projection workers and external search should follow measured workloads; any asynchronous projection must retain authorization checks at query time and define its consistency contract.

## Migration guarantees and verification

The foundation migration adds tables and columns without replacing the existing resource table or losing its triggers. It reconstructs available published and current draft history, preserves manual publishing for legacy collections, and refuses to discard legacy denies. Existing history cannot reconstruct previously overwritten drafts or historical schema changes that were never saved.

Tests apply the actual versioned migrations. Coverage includes populated upgrades, deny-guard rollback, ownership constraints, exact values through REST, head/published search and blobs, typed index rebuilds, schema/history immutability, authorized pagination, and concurrent optimistic updates. Representative load benchmarks, restoration exercises, group provisioning, workflow approval, quotas, malware scanning, idempotency keys and observability exports remain deployment/application work.

## Application configuration

Workspace-owned schema templates, taxonomy, and relationship types use workspace authorization. Collection-owned views use collection authorization. Applying a template explicitly adopts its checked version into one immutable effective schema revision; existing content is never rewritten. Rich field options and defaults are retained in those snapshots. Multiplicity and reference scope are field identity, with ordered multi-value indexes for each visible surface.

The bounded query specification compiles typed predicates, visible-surface sort keys, totals, and group counts into parameterized SQL. Authorization happens before aggregation and pagination. Query selection and hydration share a snapshot; cursors bind caller, schema, collection, surface, and query. Saved views grant no access. [API behavior and examples](application-foundation.md) describe these contracts.

The application-foundation migration preserves old augmented inheritance by copying its effective grant union into an exclusive scope and auditing that conversion. Those scopes become independent of subsequent ancestor grant changes.
