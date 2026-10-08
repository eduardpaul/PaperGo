# Reusable application foundation

PaperGo 0.3 adds rich fields, reusable schema templates, saved views, controlled taxonomy, and typed relationships. The deployment remains one organization with multiple workspaces, SQLite, Ent, and Atlas.

## Permission scopes

New lists, libraries, folders, and items inherit their parent's effective permissions. The workspace is the initial exclusive scope. Authorization follows ancestors until the nearest resource with `inherit_permissions=false`; only that scope's grants apply. Grants within a scope are additive, and denies are rejected.

An exclusive folder scope affects its inherited descendants immediately. A descendant with its own exclusive scope remains independent. Scope changes and authorization reads use transactions; no background permission propagation is involved.

Use PUT `/v1/resources/{id}/permissions` with the resource's quoted numeric If-Match:

- Create or replace an exclusive scope: `{"inherit":false,"grants":[{"subject":"admin","action":"manage"},{"subject":"reader","action":"read"}]}`.
- Break inheritance and copy effective parent permissions: `{"inherit":false,"copy_inherited":true}`. Additional grants can be included. Copies become independent of later parent changes.
- Reset to inherit: `{"inherit":true,"grants":[]}`. Local grants are removed.
- Inherited resources cannot contain local grants. Changes that remove the caller's manage access roll back.

GET permissions requires manage access and returns `inherit`, local `grants`, the effective `scope_id`, and `effective_grants`. Read, read_draft, write, publish, and manage retain their existing capability implications. Ordinary readers see only published content.

## Rich fields

The field registry supports `text`, `note`, `email`, `url`, `date`, `datetime`, `choice`, `integer`, `decimal`, `number`, `boolean`, `lookup`, and `term`.

Field creation retains key, label, type, required, choices, indexed, and decimal scale. Optional configuration belongs in `options`:

```json
{
  "key": "amount",
  "label": "Amount",
  "type": "decimal",
  "scale": 2,
  "indexed": true,
  "required": true,
  "options": {
    "description": "Invoice total",
    "minimum": "0.00",
    "maximum": "1000000.00",
    "default_value": "0.00"
  }
}
```

Options include description, default_value (a JSON value), max_length, minimum/maximum (exact numeric strings), multiple, lookup_container_id, term_set_id, and unique. Text/choice/reference fields can have multiple values; arrays contain at most 100 non-null members and duplicate normalized values are removed. Required arrays must remain nonempty.

Email fields accept a plain address; URL fields accept an absolute HTTP(S) URL without credentials; date fields use YYYY-MM-DD; datetimes normalize to UTC. Defaults fill absent fields when writing a revision; explicit null remains null for optional fields. Defaults are validated before saving field definitions. Required fields can be added to populated collections when they have a valid default, without rewriting old revisions.

Integer and decimal bounds, defaults, history, and indexed comparisons preserve exact precision. Decimal content is a canonical fixed-scale string; its index is signed 64-bit scaled units. The existing approximate `number` type remains available.

Lookup fields require a same-workspace collection and validate newly assigned targets as visible items in that collection. Term fields require a same-workspace term set and stable term IDs. Unchanged references can remain in later revisions when a term is deprecated or lookup access changes.

Keys, types, scale, multiplicity, and reference scope are immutable. PATCH field `options` is a complete replacement, retaining those identity properties. Other validation/display options evolve through frozen SchemaRevision snapshots. Index flags still rebuild head/published projections atomically; multiple values receive separate ordinal index rows.

## Reusable schema templates

Templates are workspace-managed schema definitions with stable keys and concurrency versions.

| Method | Route | Purpose |
| --- | --- | --- |
| GET / POST | /v1/workspaces/{id}/templates | List or create templates |
| GET / PUT | /v1/templates/{id} | Read or replace a template |
| POST | /v1/resources/{id}/templates/{templateID}/apply | Explicitly adopt a template into a collection |

Create a template with key, name, optional description, a fields array, and optional cross-field rules. Template fields cannot select a content type. Updating a template requires its own If-Match and does not change consumers.

Applying requires the current collection If-Match and `{"template_version":1,"content_type_id":"..."}`; omitting content_type_id selects the default type. Adoption adds fields to that type, updates compatible shared catalog definitions, merges rules by stable key, rebuilds query projections and business key claims, and records exactly one new effective schema revision in one transaction. Other type memberships remain intact. Incompatible field identities, invalid existing heads, duplicate keys or stale versions reject the entire operation. Audit events record the template ID/version, content type and resulting schema revision.

Existing item revisions keep their frozen schema. Later content edits adopt their type within the collection's effective schema; publishing an older revision does not reinterpret it. Templates can be reused across collections within their workspace.

## Content types, business keys and cross-field rules

Each collection has a shared catalog of at most 200 fields and up to 32 content types. A new collection has one default type with key `item`. Creating a field assigns it to the selected `content_type_id`, or the default type when omitted. Types select catalog keys with `field_keys`; a shared key has one field definition across the collection. Each item has an immutable `content_type_id`; omitted on creation, it selects the current default. Bulk creates accept the same selector, and every bulk content update validates against its assigned type.

| Method | Route | Concurrency |
| --- | --- | --- |
| GET / POST | /v1/resources/{id}/content-types | Creation requires collection If-Match |
| GET / PUT | /v1/content-types/{id} | Replacement requires content type If-Match |
| DELETE | /v1/resources/{id}/fields/{fieldID} | Collection If-Match; returns the new collection ETag |

Content type creation and replacement require manage access. Bodies contain key (required on creation, immutable), name, field_keys, optional rules and is_default. Exactly one type is default. Selecting a new default advances the former default's version; replacing the current default cannot clear it until another type is selected. Field and rule changes validate existing heads in bounded batches before recording the schema. Required fields and rules apply only to their assigned types. Frozen collection schemas retain every type's ID, keys and rules, and each item revision records its type ID.

`options.unique=true` creates a collection-wide business key. It requires an indexed scalar field and excludes approximate `number` and multiple values. Equality is exact and case-sensitive after field normalization; integers and decimals retain full precision. Optional nulls reserve nothing. Both head and published values remain reserved by an item; a draft change releases its former key only when that value leaves both surfaces. Unpublishing and deletion release the applicable claims. Enabling uniqueness validates existing surfaces atomically. Collisions return 409; bulk errors identify the failing operation and roll back all mutations. Constraints are checked in operation order, so key swaps that temporarily collide are rejected.

Cross-field validation supports up to 32 declarative rules per type. A rule has a stable key, field, op and message. Comparisons use other_field of the same scalar type and decimal scale and support eq/ne/gt/gte/lt/lte; ordered operators exclude boolean, choice, lookup and term. Comparisons skip null operands. Conditional requirements use `{"key":"approval_code","field":"code","op":"required_if","when_field":"approved","when_value":true,"message":"Approved items need a code"}`. Defaults and field normalization run before rules on every new content revision. Templates can define and adopt these rules.

Deleting a field removes it from the active catalog, all type memberships, query indexes and key claims. Rules and saved views that reference it must first be edited or removed. Immutable content and schema revisions retain their original values and definitions. Later edits or blob uploads omit removed values when carrying content forward; explicit replacement payloads reject undeclared keys. Removed keys cannot be reused.

## Typed queries and saved views

POST `/v1/resources/{collectionID}/query` executes a bounded collection-wide item query, including items nested in folders:

```json
{
  "query": {
    "filter": {
      "and": [
        {"field": "amount", "op": "gte", "value": "125.00"},
        {"field": "status", "op": "in", "value": ["approved", "paid"]}
      ]
    },
    "sort": {"field": "amount", "direction": "desc"},
    "group_by": "status"
  },
  "surface": "auto",
  "limit": 50
}
```

The response contains resource `data`, authorized `total` before pagination, and optional `next_cursor`. Include the returned cursor as `after`. Cursors bind the caller, collection, effective schema, query, and surface. They use keyset pagination with an ID tie-breaker and missing values last. Changing those inputs requires restarting pagination. Concurrent edits can move items between pages; the snapshot guarantee applies to each request.

A filter node is one condition or an AND/OR/NOT group, with at most 32 nodes and depth 6. Operators: eq, ne, gt/gte/lt/lte, in, contains, missing, present. Operator/type compatibility is enforced. Custom fields must be indexed; system fields use `$id`, `$name`, and `$tags` to avoid collisions with custom keys. Multi-value equality matches any member; ne means present with no equal member. Sort/group fields must be scalar. Default sort is $id ascending.

Optional query content_type_id restricts items and usable query fields to one collection type. Omitting it queries all types with the shared catalog. Optional query parent_id restricts direct children of a collection or one of its folders. Search is a literal FTS phrase, and tag applies to the selected content surface. Missing optional values have no typed index row.

POST the same request to `/query/groups` for a separately paginated collection of `{"value":...,"count":...}` groups. Counts use all authorized matching items before pagination. Integer and decimal group keys are exact strings, boolean keys are booleans, and missing keys are null. Group cursors cannot be used as row cursors.

Permissions and surface selection are applied in SQL before sorting, pagination, counts, and grouping. All selection, counts, and hydration share one snapshot. Auto selects head for draft readers and published for ordinary readers. Explicit head selection requires draft access for returned items.

| Method | Route | Purpose |
| --- | --- | --- |
| GET / POST | /v1/resources/{id}/views | List or create collection views |
| GET / PUT / DELETE | /v1/views/{id} | Read, replace, or remove a view |
| POST | /v1/views/{id}/query | Execute saved row query |
| POST | /v1/views/{id}/query/groups | Execute saved group query |

A view stores name, columns, query, layout (table/board/calendar/gallery), and is_default. These layouts are hints for headless clients. Columns select custom values and optionally $tags; identity/name remain available in resource responses. Empty columns default to $name. Only one default view per collection is allowed; replacing the default advances the previous default view's version.

View management requires collection manage permission. Reading and execution require read, and saved configuration grants no access to content. PUT and DELETE use the view's ETag. View execution accepts only surface, after, and limit; the stored query controls filtering and sorting.

## Bulk item operations

POST `/v1/resources/{collectionID}/bulk` applies 1..100 operations to items in one list or library. The request body is limited to 1 MiB. All operations execute in request order under one writer acquisition and one transaction; any failure rolls back resources, revisions, publication events, relationships, projections, and audit events together. Collection read access is required, followed by the same per-item permissions and validation as individual mutations.

```json
{
  "operations": [
    {
      "action": "create",
      "create": {"name": "New invoice", "values": {"amount": "125.00"}}
    },
    {
      "action": "update",
      "id": "ITEM_ID",
      "version": 3,
      "update": {"tags": ["reviewed"], "parent_id": "FOLDER_ID"}
    },
    {"action": "publish", "id": "ANOTHER_ITEM_ID", "version": 2}
  ]
}
```

Actions:

- `create`: requires `create` with name and optional tags/values. Optional top-level `parent_id` chooses an existing folder in the collection; omission creates directly in the collection. Defaults and automatic publication follow collection settings. Library items start without a blob, uploaded through the content endpoint.
- `update`: requires id, positive current resource version, and `update` with name, tags, values, or parent_id. Values replace the complete custom-value object; moves stay within the collection. Collection settings cannot be changed through bulk operations.
- `publish`, `unpublish`, `delete`: require id and positive current resource version, with no create/update payload. Explicit publication still requires an enabled publication policy and, for library items, an attached blob.

Each existing item may appear only once in a request. Targets must be items, so folders and their potentially unbounded descendants are excluded. Newly created items cannot be referenced by later operations in the same request. Submit larger workloads as separate batches; each batch commits independently.

Success returns HTTP 200 with `{"data":[{"action":"create","id":"NEW_ITEM_ID","version":1},...]}` in request order. Existing-item operations return the consumed version plus one, including deletes. Results omit content payloads. There is no response ETag or collection-level If-Match: each existing-item operation supplies its own numeric `version`.

Operation failures return the usual problem status/code (for example 403, 404, 409, or 422) plus a zero-based `operation_index`; no partial results are returned. Invalid batch size and collection-level failures have no operation index. Unknown JSON fields and malformed bodies return 400. A rejected batch leaves no changes and can be corrected and resubmitted. Bulk requests do not deduplicate retries: a successful create submitted again creates another item, while repeating an existing-item mutation with its old version conflicts.

Run `go test ./internal/dms -run '^$' -bench BenchmarkItemUpdates -benchmem` to compare individual and bulk updates at 1, 10, and 100 items per workload. The benchmark uses indexed exact integers, automatic publication, retained revisions, and the actual SQLite schema; it reports items per second and allocations, excluding fixture setup.

## Controlled taxonomy

| Method | Route | Purpose |
| --- | --- | --- |
| GET / POST | /v1/workspaces/{id}/term-sets | List or create term sets |
| GET / PUT | /v1/term-sets/{id} | Read or replace term set metadata |
| GET / POST | /v1/term-sets/{id}/terms | Search/list or create terms |
| GET / PUT | /v1/terms/{id} | Read or replace term metadata |

Term sets have stable keys, names, descriptions, and versions. Terms have stable IDs, immutable optional parents within the same set, names, localized labels, synonyms, deprecation state, and versions. Hierarchy depth is bounded. Names are unique case-insensitively within a term set. Term listing supports q searches across names, labels, and synonyms and ID keyset pagination.

Management requires workspace manage; reads require workspace read. PUT requires the entity's ETag and replaces its mutable metadata. Deprecated terms cannot be newly assigned, but existing references and historical content remain intact. Labels resolve from the current taxonomy; term definitions themselves are not immutable content-history snapshots. Free tags continue to work independently.

## Typed relationships

| Method | Route | Purpose |
| --- | --- | --- |
| GET / POST | /v1/workspaces/{id}/relationship-types | List or create policies |
| GET / PUT | /v1/relationship-types/{id} | Read or replace policy metadata |
| PATCH | /v1/items/{id}/relationships/{linkID} | Replace validated edge metadata |

A relationship type has key, label, inverse_label, explicit directed flag, optional max_incoming/max_outgoing (1..1000), attribute definitions, and version. Attributes use unindexed scalar field definitions, including defaults and validation. Reference/multiple attributes are rejected. An empty attribute schema permits no custom keys.

Directed=false creates symmetric links: endpoints are stored in canonical order, reverse duplicates are rejected, and incoming/outgoing queries include either endpoint. Symmetric types cannot have directional cardinality or inverse labels.

POST an item's existing relationships endpoint with:

```json
{"type_id":"TYPE_ID","target_id":"TARGET_ID","metadata":{"weight":"1.20"}}
```

The type supplies the stable relationship name. Typed attributes are validated and normalized; cardinality allocation is transactional. Keys and direction are immutable. Attribute schemas cannot change while edges use a type; label changes are allowed. Lowering cardinality cannot invalidate existing edges.

Every relationship requires a type. Links stay within one workspace, with source write and target read on creation. For symmetric edges, either endpoint can act as the authorized source when editing or removing the edge. Query visibility still requires both endpoints to have authorized visible content.

Creation returns an edge ETag. PATCH and DELETE require the edge If-Match. PATCH replaces metadata rather than merging it. Edge versions are independent of item lock versions. Edges remain live and are not frozen by item publication.

## Verification

Tests cover foreign-key/integrity checks, exact defaults and filters, schema pairing, template adoption rollback, multi-value index rebuilds, taxonomy/reference validation, exclusive-scope copy/reset, reader/draft query visibility, counts/groups/pagination, view concurrency, typed attributes, symmetric duplicates, and concurrent cardinality. The OpenAPI contract documents all registered routes.


## Indexed system metadata

Queries, grouping and saved views accept $created_at, $created_by, $modified_at, and $modified_by alongside $id, $name and $tags. Modification time and actor describe the selected immutable content revision: readers cannot infer draft actors or times. Creation metadata describes item creation. Times accept RFC3339 values and normalize to UTC with nanosecond precision. Dedicated collection/surface indexes cover these metadata fields; no custom field configuration is needed.

