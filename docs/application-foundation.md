# Reusable application foundation

PaperGo 0.3 adds rich fields, reusable schema templates, saved views, controlled taxonomy, and typed relationships. The deployment remains one organization with multiple workspaces, SQLite, Ent, and Atlas.

## Permission scopes

New lists, libraries, folders, and items inherit their parent's effective permissions. [Workspace memberships](workspaces.md) provide the initial exclusive scope. Authorization follows ancestors until the nearest resource with `inherit_permissions=false`; its grants apply, and workspace owners retain control of every descendant. Grants within a scope are additive, and denies are rejected.

An exclusive folder scope affects its inherited descendants immediately. A descendant with its own exclusive scope remains independent. Scope changes and authorization reads use transactions; no background permission propagation is involved.

Use PUT `/v1/resources/{id}/permissions` for collections and content with the resource's quoted numeric If-Match. Workspace roots use the membership API:

- Create or replace an exclusive scope: `{"inherit":false,"grants":[{"principal_type":"user","principal_id":"admin","action":"manage"},{"principal_type":"group","principal_id":"group-id","action":"read"}]}`. User subjects and group IDs must exist in the organization directory.
- Break inheritance and copy effective parent permissions: `{"inherit":false,"copy_inherited":true}`. Additional grants can be included. Copies remain independent of parent ACL edits, while group and workspace-role membership resolve dynamically.
- Reset to inherit: `{"inherit":true,"grants":[]}`. Local grants are removed.
- Inherited resources cannot contain local grants. Changes that remove the caller's manage access roll back.

GET permissions requires manage access and returns `inherit`, local `grants`, the effective `scope_id`, and `effective_grants`. Read, read_draft, write, publish, and manage retain their existing capability implications. Current item and file reads select published content for ordinary readers. Metadata history list/get requires ordinary item read and list.read scope and includes all retained revisions, including unpublished revisions, with frozen schemas. Current head reads require read_draft, and restoring a revision requires write. Collections configure `versioning` (`off` or `major`) and `max_versions` (1–500, default 50) on create/PATCH. Lists default off and libraries major. Off stops new history entries; retained entries remain readable/restorable. Each recorded change trims the item to its configured count. Retention removes unused metadata snapshots after workflow event dispatch. Head, published and retained-history revisions remain; queued events temporarily retain their revision and its predecessor for term-change triggers. Publication audit records retain independent immutable snapshots.

Principals are `user`, `group`, `workspace_visitor`, `workspace_member` or `workspace_owner`. Workspace role principals identify the resource's workspace; owner grants must allow manage. Copying the workspace scope preserves its three role principals. Resetting inheritance restores the current parent scope and leaves nested exclusive scopes intact. [Module scopes](identity.md) apply independently: `list.read` permits list access and permission management, while `list.write` permits item/folder mutations and publishing. Content-type/schema curation requires `contentType.manage`; organization taxonomy operations require `taxonomy.read` or `taxonomy.manage`, independently of workspace membership and resource ACLs. REST, WebDAV and transactional workflow item actions resolve persisted memberships and roles.

## Rich fields

The field registry supports `text`, `note`, `email`, `url`, `date`, `datetime`, `choice`, `integer`, `decimal`, `number`, `currency`, `boolean`, `person`, `lookup`, `term`, and `keywords`.

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

Options include description, default_value (a JSON value), max_length, minimum/maximum (exact numeric strings), multiple, lookup_container_id, term_set_id, currency_code, and unique. Only choice/person/lookup/term/keywords fields can have multiple values; arrays contain at most 100 non-null members and duplicate normalized values are removed. Required arrays must remain nonempty.

Text/email values default to 255 UTF-16 code units, URLs to 2048 and notes to 100000. `options.max_length` permits 1..4000 for text/email/URL and 1..1000000 for notes. Non-BMP characters count as two units. The encoded field-value budget is 8 MiB and JSON request-body budget is 16 MiB, allowing a full-size note. Email fields accept a plain address; URL fields accept an absolute HTTP(S) URL, including credentials and case-insensitive schemes; date fields use YYYY-MM-DD; datetimes normalize to UTC. Defaults fill absent fields when creating an item. Item PATCH merges supplied fields into current values; omitted keys remain, and null removes an optional field without reapplying its default. Defaults are validated before saving field definitions. Required fields can be added to populated collections when they have a valid default, without rewriting old revisions.

Integer and decimal bounds, defaults, history, and indexed comparisons preserve exact precision. Decimal content is a canonical fixed-scale string; its index is signed 64-bit scaled units. `number` and `currency` use exact JSON decimals with a 96-bit coefficient and scale 0..28, ranging from -79228162514264337593543950335 to 79228162514264337593543950335. Excess fractional precision rounds to nearest, with midpoint ties to even; very small values round to zero. Currency fields require `options.currency_code` containing three uppercase letters (for example EUR). Numeric bounds, filters, sorting, grouping and pagination use exact values. SQLite indexes store a fixed-width numeric ordering key without conversion to floating point.

Person fields store enrolled OIDC subjects and validate newly assigned values against active organization users. GET `/v1/people` lists active subjects with `list.read`; a person assignment grants no content access. Person defaults use the same active-user validation. Explicit reassignment validates every supplied person; omitted people remain in unrelated edits.

Lookup fields reference live collections in any workspace and validate assigned target existence in that collection, independently of target read permission. Unchanged references can remain in later revisions when a term is deprecated or lookup access changes.

Term fields reference organization term sets shared across workspaces; keywords fields take the organization's keywords (terms of its keywords set and terms available as keywords) and must be indexed. Both store term IDs, and writers may give a term's ID or its label instead. A term field matches a label against the names, then the localized labels, then the synonyms of the set's active terms; a label that matches several terms is rejected (give the ID), an unknown label requires `taxonomy.read` to add a root term to an open set and is rejected by a closed one. A keywords field finds the keyword with that name or synonym, or adds it to the keywords set with `taxonomy.read`. An ID of a merged term is stored as the term it was merged into, and every write of an item replaces the merged terms it still holds. Deprecated terms cannot be newly assigned. The names, labels and synonyms of an item's terms are part of its full-text search text, as they were when the item was last written.

Keys, types, scale, multiplicity, and reference scope are immutable. PATCH field `options` is a complete replacement, retaining those identity properties. Other validation/display options evolve through frozen SchemaRevision snapshots. Changing `indexed` on a populated collection commits at once, sets the field's `index_status` to `building` and queues a `field_index` operation; multiple values receive separate ordinal index rows. The operation converges the field's index rows across head and published surfaces in batches of 100 surfaces, each in its own short write transaction, so other writes proceed between batches and edits made meanwhile index themselves. Queries reject the field until `index_status` is `ready`; a failed build sets it to `failed`, and changing the flag again queues a fresh build that supersedes any pending one. Track progress with `GET /v1/resources/{id}/operations` or `GET /v1/operations/{id}`; builds resume after a restart.

## Reusable schema templates

Templates are workspace-managed schema definitions with stable keys and concurrency versions.

| Method | Route | Purpose |
| --- | --- | --- |
| GET / POST | /v1/workspaces/{id}/templates | List or create templates |
| GET / PUT | /v1/templates/{id} | Read or replace a template |
| POST | /v1/resources/{id}/templates/{templateID}/apply | Explicitly adopt a template into a collection |

Create a template with key, name, optional description, a fields array, and optional cross-field rules. Template fields cannot select a content type. Updating a template requires its own If-Match and does not change consumers.

Applying requires the current collection If-Match and `{"template_version":1,"content_type_id":"..."}`; omitting content_type_id selects the default type. Adoption adds fields to that type, updates compatible shared catalog definitions, merges rules by stable key, rebuilds business key claims, queues index builds for fields whose `indexed` flag changed, and records exactly one new effective schema revision in one transaction. Other type memberships remain intact. Incompatible field identities, invalid existing heads, duplicate keys or stale versions reject the entire operation. Audit events record the template ID/version, content type and resulting schema revision.

Existing item revisions keep their frozen schema. Later content edits adopt their type within the collection's effective schema; publishing an older revision does not reinterpret it. Templates can be reused across collections within their workspace.

## Content types, business keys and cross-field rules

Content types and field definitions form an organization catalog. Types reference immutable field IDs with `field_ids`; collections attach type IDs without copying. Each collection resolves at most 200 fields and 32 types. Types sharing a field key within a collection must use the same catalog field ID. Creating a collection with `content_type_ids` attaches those definitions, with the first as default. Omitting this list creates an editable `Item` catalog definition and attaches it as default. Each item and folder has an attached `content_type_id`; omission on creation selects its collection's default. PATCH can change the content type, retaining shared fields and pruning fields absent from the destination type. Folder fields are optional and receive no defaults; their metadata is live and has no public version history. Bulk creation and content updates use the same resolution.

| Method | Route | Concurrency |
| --- | --- | --- |
| GET | /v1/content-types/audit | Catalog curation audit; contentType.manage |
| GET / POST | /v1/content-types | Organization catalog; creation needs no collection ETag |
| GET / PUT | /v1/content-types/{id} | Replacement requires content type If-Match |
| GET / POST | /v1/fields | Organization field catalog |
| PATCH / DELETE | /v1/fields/{id} | Field If-Match |
| GET / POST | /v1/resources/{id}/content-types | POST attaches by ID using collection If-Match |
| DELETE | /v1/resources/{id}/fields/{fieldID} | Collection If-Match; returns the new collection ETag |

Catalog reads require `contentType.read`; curation requires `contentType.manage`, independently of workspace membership. Content type bodies contain key (required on creation, immutable), name, field_ids and optional rules. Attachment bodies contain `content_type_id` and optional `is_default`; attachment requires `list.read`, `contentType.read` and collection manage access. Exactly one attachment is default. Selecting a default changes only that collection and its ETag.

Editing a catalog type or field validates every attached collection's existing heads and saved-view dependencies before advancing all effective schema revisions in one transaction. A validation failure anywhere rolls back every definition, schema revision, ETag, key claim and audit event. Field changes also advance all referencing type versions. Index changes enqueue work for every populated attached collection; the shared field remains building until every operation completes. Collection field creation, patching, deletion and template adoption edit these same live catalog definitions.

Required fields and rules apply only to assigned types. Frozen collection schemas retain resolved fields, type IDs, keys and rules; each immutable item revision pins its effective schema. Shared updates never rewrite historical or published revisions. Lookup fields retain their workspace reference boundary, so a type with a lookup can attach only in its lookup target's workspace.

`options.unique=true` creates a collection-wide business key. It requires an indexed scalar field and excludes multiple values. Equality is exact and case-sensitive after field normalization; integers and decimals retain full precision. Optional nulls reserve nothing. Both head and published values remain reserved by an item; a draft change releases its former key only when that value leaves both surfaces. Unpublishing and deletion release the applicable claims. Enabling uniqueness validates existing surfaces atomically. Collisions return 409; bulk errors identify the failing operation and roll back all mutations. Constraints are checked in operation order, so key swaps that temporarily collide are rejected.

Cross-field validation supports up to 32 declarative rules per type. A rule has a stable key, field, op and message. Comparisons use other_field of the same scalar type and decimal scale and support eq/ne/gt/gte/lt/lte; ordered operators exclude boolean, choice, lookup, term and keywords. Comparisons skip null operands. Conditional requirements use `{"key":"approval_code","field":"code","op":"required_if","when_field":"approved","when_value":true,"message":"Approved items need a code"}`. Defaults and field normalization run before rules on every new content revision. Templates can define and adopt these rules.

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
    "sort":[{"field": "amount", "direction": "desc"}],
    "group_by": "status"
  },
  "surface": "auto"
}
```

The response contains resource `value` and `@odata.nextLink` (null on the final page). Set `$top` in the URL to select a page size (default 50, clamped to 1–500). Follow the returned link using POST with the same JSON body. The URL carries the opaque `$skiptoken`; paging is not part of the query body. Set `"include_total": true` to receive the authorized match count in `total`; it evaluates every match. `sort` is an ordered array of up to eight unique field/direction pairs, with independent `asc` or `desc` directions. Custom sort keys require ready scalar indexes. An omitted or empty array uses `$id` ascending. Missing values sort last at each key; the final ID tie follows the primary direction. Single-key pages scan the sort index; multiple keys sort the authorized matching set and use a typed lexicographic keyset cursor. Cursors bind the caller, collection, schema, surface, query and selection. Changing these inputs restarts pagination. Concurrent edits can move items between pages; snapshot guarantees apply to each request.

`select` is an optional array of up to 50 unique known field keys on direct and saved-view query requests. It projects custom values in SQL, including unindexed fields; `$tags` includes tags. The resource metadata envelope remains present. Omitted selection returns all values on a direct query and the saved columns on a view. An explicit empty array returns no custom values. Ad hoc selection overrides saved-view columns. Selection does not change filtering, sorting or counts. Group queries do not accept `select`.

A filter node is one condition or an AND/OR/NOT group, with at most 32 nodes and depth 6. Operators: eq, ne, gt/gte/lt/lte, in, contains, under, missing, present. Operator/type compatibility is enforced. Term and keywords fields support eq, ne, in and under (the term or any of its descendants); a value that holds a merged term matches as the term it was merged into. Custom fields must be indexed; system fields use `$id`, `$name`, and `$tags` to avoid collisions with custom keys. Multi-value equality matches any member; ne means present with no equal member. Sort/group fields must be scalar. Default sort is $id ascending.

Optional query content_type_id restricts items and usable query fields to one collection type. Omitting it queries all types with the shared catalog. Optional query parent_id restricts direct children of a collection or one of its folders. Search is a literal FTS phrase, and tag applies to the selected content surface. Missing optional values have no typed index row.

POST the same request to `/query/groups` for a separately paginated collection of `{"value":...,"count":...}` groups. Counts use all authorized matching items before pagination. Term and keywords groups carry the term's name in `label` when the caller has `taxonomy.read`. Integer and decimal group keys are exact strings, boolean keys are booleans, and missing keys are null. Group cursors cannot be used as row cursors.

Permissions and surface selection are applied in SQL before sorting, pagination, counts, and grouping. All selection, counts, and hydration share one snapshot. Auto selects head for draft readers and published for ordinary readers. Explicit head selection requires draft access for returned items.

| Method | Route | Purpose |
| --- | --- | --- |
| GET / POST | /v1/resources/{id}/views | List or create collection views |
| GET / PUT / DELETE | /v1/views/{id} | Read, replace, or remove a view |
| POST | /v1/views/{id}/query | Execute saved row query |
| POST | /v1/views/{id}/query/groups | Execute saved group query |

A view stores name, columns, query, layout (table/board/calendar/gallery), and is_default. These layouts are hints for headless clients. Columns select custom values and optionally $tags; identity/name remain available in resource responses. View queries read only those values from the selected surface, so rows stay small however large item content is; fetch an item for its full content. Empty columns default to $name. Only one default view per collection is allowed; replacing the default advances the previous default view's version.

View management requires collection manage permission. Reading and execution require read, and saved configuration grants no access to content. PUT and DELETE use the view's ETag. View execution accepts surface, after, limit, include_total and an optional select override; the stored query controls filtering and sorting.

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

Operation failures return the usual problem status/code (for example 403, 404, 409, 412, or 422) plus a zero-based `operation_index`; no partial results are returned. Invalid batch size and collection-level failures have no operation index. Unknown JSON fields and malformed bodies return 400. A rejected batch leaves no changes and can be corrected and resubmitted. Bulk requests do not deduplicate retries: a successful create submitted again creates another item, while repeating an existing-item mutation with its old version fails with 412.

Run `go test ./internal/dms -run '^$' -bench BenchmarkItemUpdates -benchmem` to compare individual and bulk updates at 1, 10, and 100 items per workload. The benchmark uses indexed exact integers, automatic publication, retained revisions, and the actual SQLite schema; it reports items per second and allocations, excluding fixture setup.

`BenchmarkCollectionQuery` and `BenchmarkWriteDuringIndexBuild` seed one collection of `PAPERGO_BENCH_ITEMS` items (default 2000) with indexed integer and text fields, missing values, half the items published, and every fiftieth item under its own ACL scope granting a reader drafts. Run `PAPERGO_BENCH_ITEMS=20000 go test ./internal/dms -run '^$' -bench 'BenchmarkCollectionQuery|BenchmarkWriteDuringIndexBuild' -benchtime=20x -benchmem`. The query benchmark reads 50-row pages for system and custom sorts in both directions, second pages by cursor, broad and selective filters, search, fragmented reader permissions and an opt-in total. The write benchmark reports mean and maximum single-item edit latency on an idle writer and while background index builds run, plus WAL size. Two costs to watch: a selective filter, or a reader who can see drafts on only a few items, walks the sort index to fill a page instead of sorting; and `include_total` evaluates every match.

## Controlled taxonomy

| Method | Route | Purpose |
| --- | --- | --- |
| GET / POST | /v1/term-groups | List or create term groups |
| GET / PUT / DELETE | /v1/term-groups/{id} | Read, replace or delete a term group |
| POST | /v1/term-groups/{id}/import | Import term sets from a SharePoint CSV (`text/csv`) |
| GET / POST | /v1/term-sets | List (`group_id` narrows) or create term sets |
| GET / PUT / DELETE | /v1/term-sets/{id} | Read, replace or delete a term set |
| GET / POST | /v1/term-sets/{id}/terms | Browse or search terms, or create one |
| GET | /v1/terms?ids= | Look up 1 to 200 terms by ID |
| GET / PUT | /v1/terms/{id} | Read or replace term metadata |
| POST | /v1/terms/{id}/move | Move a term and its subtree |
| POST | /v1/terms/{id}/merge | Merge a term into another |
| GET / POST | /v1/keywords | Suggest keywords, or get or add one |
| GET | /v1/keywords/popular | Most used keywords |
| POST | /v1/keywords/{id}/promote | Promote a keyword into a managed set |
| GET | /v1/taxonomy/audit | Organization curation audit (taxonomy.manage) |
| GET | /v1/taxonomy/export | Export the taxonomy as a package |
| POST | /v1/taxonomy/import | Import a taxonomy package |

**Groups and sets.** Term groups organize the organization's shared term sets; group names and set keys are unique across the organization. The organization has one system group, which holds its keywords set and cannot be renamed, deleted or given other sets. A term set has a stable key, a name unique in its group, a description and `is_open`. Open sets accept new terms from people with `taxonomy.read`; closed sets change only with `taxonomy.manage`. Empty groups (other than the system group) and term sets without terms or fields can be deleted.

**Terms.** Terms have stable IDs, a parent in the same set (or none), a name unique among the active children of a parent (case-insensitively), a description, an optional `#rrggbb` color, a `sort_order`, localized labels, synonyms and a deprecation flag. `path` lists the IDs from the root to the term (`/root/…/term/`), so a subtree is one index range; hierarchy depth is at most 32. Terms are never deleted: they are deprecated, which keeps existing values but rejects new assignments, or merged.

Listing `GET /v1/term-sets/{id}/terms` returns the root terms, or the children of `parent_id`, ordered by `sort_order` then name with a keyset cursor; each term carries `has_children`. `q` searches names, labels and synonyms in the whole set (below `parent_id` when given). Deprecated terms are left out unless `include_deprecated=true`; merged terms are never listed. `GET /v1/terms?ids=` returns the readable terms among up to 200 IDs in the order asked, merged ones included.

**Move and merge.** Moving a term (its ETag in `If-Match`) re-parents it within its set, never below itself, and updates the paths of its subtree. Merging a term into another active term of its set moves the source's children to the target, adds the source's name and synonyms to the target's synonyms, re-points terms earlier merged into the source, and keeps the source as a deprecated term whose `merged_into_id` names the target. Item values that hold a merged term keep its ID until the item is next written; filters treat it as the target, and readers resolve it through `merged_into_id`. A merge raises the `term.merged` domain event (`source_term_id`, `target_term_id`, `term_set_id`).

**Keywords.** The keywords set is the open set of free keywords. People with `taxonomy.read` get or add a keyword with `POST /v1/keywords {"name": ...}`: an active keyword with that name or synonym (any case) comes back with 200, else a new root keyword with 201. `GET /v1/keywords?q=` suggests up to 20 keywords, names that start with `q` first. Managers see the most used keywords with `/keywords/popular?top=` (items whose current content holds them in indexed term fields of the keywords set) and promote a keyword with `POST /v1/keywords/{id}/promote {"term_set_id", "parent_id"}`: when the target set has an active term with the keyword's name, label or synonym, the keyword merges into it; otherwise the keyword moves there. Either way the resulting term is `available_as_keyword`, so keyword suggestions and keyword pickers still offer it. Managers can also mark any term `available_as_keyword`.

**CSV import.** `POST /v1/term-groups/{id}/import` reads the SharePoint term set format (at most 1 MiB): `Term Set Name`, `Term Set Description`, `LCID`, `Available for Tagging`, `Term Description` and `Level 1 Term` to `Level 7 Term`. A row with a term set name starts that set; each row adds the path of terms in its level columns. Import is additive: the group's sets with the same name and terms with the same name under the same parent are reused. New sets are closed and get a key derived from their name. A term's description, and `Available for Tagging` FALSE (imported deprecated), apply when the import creates it. The result lists the sets and counts what was created.

**Packages.** `GET /v1/taxonomy/export` returns the taxonomy by name, for setting it up in another organization deployment: `groups` (the system group marked `system`) with their `sets` (key, name, description, `is_open`) and nested `terms` (name, description, color, sort order, labels, synonyms, deprecation, `available_as_keyword`, `children`). Merged terms are left out; IDs never appear. `POST /v1/taxonomy/import` (manage) is additive and atomic: groups match by name (the system group by role), sets by key and terms by name under the same parent; what exists stays as it is and what is missing is created. It reports `groups_created`, `sets_created` and `terms_created`. Smart folder packages refer to terms by set key and path, so importing the taxonomy first makes them portable.

Curation, closed-set creation, imports, popularity counts and audit reads require `taxonomy.manage`; reads and open-set contributions require `taxonomy.read`. Workspace membership and resource ACLs do not govern the term store. `GET /v1/taxonomy/audit` lists organization taxonomy mutations. Popularity counts span indexed content across all live workspaces. PUT, move, merge, promote and DELETE require the entity's ETag. Labels resolve from the current taxonomy; term definitions themselves are not immutable content-history snapshots. Free tags are separate plain-text labels.

## Typed relationships

| Method | Route | Purpose |
| --- | --- | --- |
| GET / POST | /v1/relationship-types | Discover predicates or ensure an immutable policy |
| GET | /v1/relationship-types/{id} | Read policy and taxonomy term |
| PATCH | /v1/items/{id}/relationships/{linkID} | Replace validated edge metadata |

A relationship type's ID is a term ID in the organization Relationships/Types vocabulary (`relationship_types`). Its name, localized labels, synonyms and deprecation are curated through taxonomy. POST accepts `name` (label or term ID), inverse_label, directed, optional max_incoming/max_outgoing (1..1000) and attributes. Reads require `list.read` and `taxonomy.read`; ensure requires `list.write` and `taxonomy.read`. Unknown labels contribute terms to the open set. Attributes use unindexed scalar field definitions, including defaults and validation. Reference/multiple attributes are rejected. An empty attribute schema permits no custom keys.

Directed=false creates symmetric links: endpoints are stored in canonical order, reverse retries return the existing edge, and an omitted direction or `both` includes either endpoint. Explicit `incoming`/`outgoing` filters select directed edges only. Symmetric types cannot have directional cardinality or inverse labels.

POST an item's existing relationships endpoint with:

```json
{"type":"TYPE_ID_OR_LABEL","target_id":"TARGET_ID","metadata":{"weight":"1.20"}}
```

Link creation resolves a term ID, name, localized label or synonym and ensures a new label when allowed. New predicates default to symmetric; optional `directed` must match an existing policy. Typed attributes are validated and cardinality allocation is transactional. Ensuring an existing policy is a no-op when semantics match and rejects changed behavior. Taxonomy term renames retain identity. Compatible term merges retarget edges and consolidate duplicates; conflicting attributes or semantics and used cardinality-bound predicates reject the entire merge.

Per-item relationship pages include a `related_item` with the peer’s selected content, workspace name and collection location. `GET /v1/items/{id}/relationships/{linkID}` reads one incident edge and returns its ETag. `GET /v1/workspaces/{id}/relationships` lists edges touching that workspace with both located endpoints. It accepts `type`, `directed`, `$top`, `$skiptoken` and `$filter`; all filters and paging run after checking visibility of both endpoints. `$filter` supports `attributes/key`, `id`, `directed`, scalar literals, `eq/ne`, numeric `gt/ge/lt/le`, `and/or/not` and parentheses. For example, `attributes/amount gt 100`. Filters are limited to 4096 bytes, 32 nodes and depth 6; nested attributes and functions are rejected.

Links may connect items across workspaces within this deployment. `type` is optional; untyped links default to symmetric, while `directed: true` creates distinct ordered edges. Untyped attributes allow up to 64 keys matching `[A-Za-z_][A-Za-z0-9_]*` (maximum 128 characters) and 16 KiB of JSON. Values must be strings, finite numbers or booleans; null removes a key. Typed links use their predicate policy and validated field schema. Creating, editing or removing an edge requires write permission on both endpoints. Either endpoint may edit or remove directed and symmetric edges. Repeating creation returns the existing edge without changing its attributes, version or audit history; the retry succeeds even when a cardinality limit is full. Query visibility still requires both endpoints to have authorized visible content.

Creation returns an edge ETag. PATCH and DELETE require the edge If-Match. PATCH merges supplied metadata keys; omitted keys remain, and null removes a key. The resulting metadata still satisfies the relationship type. Edge versions are independent of item lock versions. Edges remain live and are not frozen by item publication.

## Verification

Tests cover foreign-key/integrity checks, exact defaults and filters, schema pairing, template adoption rollback, multi-value index rebuilds, taxonomy/reference validation, exclusive-scope copy/reset, reader/draft query visibility, counts/groups/pagination, view concurrency, typed attributes, symmetric duplicates, and concurrent cardinality. The OpenAPI contract documents all registered routes.


## Indexed system metadata

Queries, grouping and saved views accept $created_at, $created_by, $modified_at, and $modified_by alongside $id, $name and $tags. Modification time and actor describe the selected immutable content revision: readers cannot infer draft actors or times. Creation metadata describes item creation. Times accept RFC3339 values and normalize to UTC with nanosecond precision. Dedicated collection/surface indexes cover these metadata fields; no custom field configuration is needed.

