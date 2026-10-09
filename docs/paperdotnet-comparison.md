# PaperDotNet entity review and PaperGo comparison

Reviewed on 2026-10-07. Repository: [eduardpaul/PaperDotNet](https://github.com/eduardpaul/PaperDotNet), commit `96df412f040f53cc14711aaba35880e24817e084`.

This is a source review of entity definitions, persistence configuration, mutation paths, and relevant architecture decisions. PaperDotNet was not built, executed, or load-tested during this review. The recommendations below are proposed adaptations; this review does not change PaperGo's runtime behavior.

## Assessment

PaperDotNet is a broader modular application platform. Its strongest contributions to our DMS/CMS foundation are reusable content types, richer field definitions, saved queries, typed relationships, durable background work, and document processing projections. PaperGo already provides a strong content consistency model: immutable revisions paired with frozen schemas, separate head and published projections, exact integer/decimal indexes, and transactional permission-aware reads.

We should reuse those platform concepts selectively while preserving the agreed deployment and publication policies in [our architecture](architecture.md): SQLite, one organization per deployment, additive grants through ancestors until an inheritance boundary, and collection-controlled automatic or explicit publication.

## Core content entities and fields

Many PaperDotNet entities include `TenantId`, creation/update actor and timestamps, and a mutable `Version` concurrency token. The tables list domain-specific fields rather than repeat all those common fields. `Version` is distinct from the numbered content/file history.

The following core definitions are implemented in [Lists entities](https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Lists/PaperDotNet.Lists/Data/Entities.cs), with persistence and indexes in [ListsDbContext](https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Lists/PaperDotNet.Lists/Data/ListsDbContext.cs).

| Entity | Purpose and principal fields | PaperGo equivalent or gap |
| --- | --- | --- |
| ContentType | Reusable schema: Id, Name, Description, IsBuiltIn, stable Key, ExtensionId, ordered Fields. | FieldDefinition and SchemaRevision are currently collection-specific; no reusable content-type catalog. |
| ListDefinition | List/library: WorkspaceId, Name, Description, Kind, AllowFolders, ordered ContentTypeIds, Versioning, TemplateKey, HasUniquePermissions, SystemKey, IndexedFields, IndexPending, MaxVersions, DeletedAt/By. | Resource with kind list/library. PaperGo adds publishing_enabled, webdav_enabled and schema_head_id; it has no content-type choices, recycle state, or optional history pruning. |
| ListItem | Row or folder: ListId, ContentTypeId, ParentId, IsFolder, Title, Fields JSON, HasUniquePermissions, ScopeId, promoted Text/Number/Date columns, DeletedAt/By. | Resource with kind item/folder, plus ItemRevision and ItemSurface. One container owns each item in both models. Deletion leaves a final `deleted_at` tombstone (the audit trail records who deleted it); there is no restore. |
| ItemVersion | Historical snapshot: ItemId, ListId, Number, ContentTypeId, Title, Fields JSON, ChangedFields, CreatedAt/By. | ItemRevision additionally pins an immutable SchemaRevision and optional Blob. |
| IndexedField | Embedded indexing plan: Field, Kind, Column, ValueField, Ready. | FieldDefinition.indexed plus typed FieldValue projections; no fixed column-slot allocation. |
| ItemValue | Multi-valued identifier index: ItemId, Field, Value GUID, ListId. | No multi-valued custom field/reference indexes yet. |
| AclEntry | Grant: ScopeId, PrincipalId, PrincipalType, Level, ListId, WorkspaceId. Principals include users, groups, and workspace roles. | Grant supports read/read_draft/write/publish/manage against resource ancestry; no group directory. |
| ItemRelation | Edge: FirstItemId, SecondItemId, TypeId, Directed, Attributes JSON, Version. | Relationship has source_id, target_id, required type_id, the type key as name, directed, version and validated metadata; always within one workspace. |
| ItemRelationshipType | Relationship policy: taxonomy-backed Id, Directed, InverseLabel, MaxIncoming, MaxOutgoing. | RelationshipType has a stable workspace key, label, inverse_label, directed flag, optional max_incoming/max_outgoing and an attribute schema; every link requires one. |
| ListView | Saved presentation/query: ListId, Name, Columns, Filter, OrderBy, GroupBy, Layout, IsDefault. | No saved view entity or compound query specification. |
| SmartFolder | Virtual collection: optional WorkspaceId/OwnerId, Name, Description, Definition JSON. | Folders are physical containment only; no cross-list saved queries. |
| ItemChange | Ordered delta marker: Sequence, ListId, ItemId, ScopeId, FromScopeId, Kind, At. | AuditEvent records actions, but there is no dedicated synchronization feed. |

`IndexedField` is embedded configuration, not a separate entity table. `FieldDefinition` below is embedded in a ContentType. PaperDotNet's folder representation is a ListItem flag; PaperGo expresses folder identity as a Resource kind.

## Field definitions

PaperDotNet's [FieldDefinition contract](https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Lists/PaperDotNet.Lists.Contracts/Fields.cs) contains:

| Concern | Fields |
| --- | --- |
| Identity and display | Name, DisplayName, Type, Description |
| Validation and defaults | Required, AllowMultiple, MaxLength, Minimum, Maximum, Choices, DefaultValue |
| Reference/configuration | LookupListId, TermSetId, CurrencyCode |
| Query behavior | Indexed, Search weight |

Its [implemented field types](https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Lists/PaperDotNet.Lists/Fields/FieldTypes.cs) are text, note, email, url, number, currency, boolean, date, dateTime, choice, person, lookup, managedMetadata, and keywords. Types register validation and normalization behavior; supported types can accept multiple values. Person, lookup, and taxonomy values validate their referenced entities.

PaperGo currently supports text, number, integer, decimal, boolean, datetime, and choice, with required/choices/indexed/decimal-scale configuration. We can expand validation and field types without sacrificing its precise integer and decimal representation. Defaults should be applied when a new revision is written, and each revision must retain the actual resulting values and effective schema.

## Documents and supporting entities

PaperDotNet separates the application platform into modules. These are the most relevant entity groups; this is a high-level domain inventory, not an exhaustive list of framework persistence tables.

| Module | Entities and principal fields | Relevance |
| --- | --- | --- |
| Organization/workspaces | Tenant: Identifier, Name, Status, Hosts. Workspace: Name, Description, PersonalOwnerId, DeletedAt/By. WorkspaceMember: WorkspaceId, UserId, Role. | Workspace membership is useful later. Tenant routing is outside our current one-organization boundary. |
| Identity | User: identity-framework fields plus DisplayName, IsDisabled, IsServiceAccount. Group, GroupMember, GroupNesting, GroupClosure. Role: Scopes, GrantsAllScopes; RoleAssignment. ApiToken: user, scopes, hash, expiry/revocation. Preferences: locale, time zone, formats, theme. | Group-based authorization is a useful enterprise extension. Keep external OIDC authentication for PaperGo. |
| Documents | StoredFile: Sha256, Size, MediaType, LastUsedAt. FileVersion: item/list/workspace, Number, IsCurrent, StoredFileId, FileName, Source, PageCount, languages. StoredFilePage: file, page number, extracted Text. LibrarySettings: DuplicatePolicy, OcrLanguages. FileCandidate: source/transformed files, workflow run, metrics, state, promoted version. GroupInbox: group and destination library. | Separate physical bytes from item-specific file versions; add extraction and transformation as background work. |
| Taxonomy | TermGroup: Name, IsSystem. TermSet: GroupId, IsOpen, IsKeywords, Key, ExtensionId. Term: TermSetId, ParentId, Name, NormalizedName, Path, multilingual Labels, Synonyms, Color, SortOrder, IsDeprecated, MergedIntoId. | Stable identifiers for governed metadata alongside today's free tags. |
| Search | SearchDocument: source/location/type/scope, Title, Keywords, Body, Language. SearchTag: DocumentId, TermId. SearchFieldValue: Name, Kind, Ordinal, Text/Number. SearchPassage: page/text, ContentHash, Embedding, EmbeddingModel, VectorStamp. | Extracted document text is valuable; semantic search can be deferred. |
| Jobs | Operation: Type, Status, PercentComplete, Payload, Result, Error, start/end times. RecurringJobState: Name, next/last run and failure state. | Durable operations/status tracking for extraction, indexing, imports, and webhooks. |
| Workflows | WorkflowDefinition and WorkflowVersion: identity, trigger, configuration snapshots. WorkflowRun: pinned workflow version, item, execution state, variables, outputs, errors, attempts, leases. WorkflowBookmark: wait/resume state. WorkflowSchedule: next execution. ApprovalRequest: assignees, due date, decision, inputs. | Approval can build on publication; a complete workflow engine is a separate product-sized feature. |
| Collaboration | Comment: item, ParentId, Text, Mentions. ActivityEntry: location, Kind, ActorId, Summary, ChangedFields, deduplication key. | Optional applications over the content core. |
| Notifications | Notification: recipient, content location, inbox/read state. Subscription and ChangeSubscription: watched scope, filters, delivery URL, expiry. WebhookDelivery and ChangeDelivery: attempts, next attempt, status, error. | Durable notification/webhook delivery rather than external calls inside content transactions. |

Sources: [tenancy](https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Tenancy/PaperDotNet.Tenancy/Data/Tenant.cs), [workspaces](https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Workspaces/PaperDotNet.Workspaces/Data/Workspace.cs), [identity](https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Identity/PaperDotNet.Identity/Data/Entities.cs), [documents](https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Documents/PaperDotNet.Documents/Data/DocumentsDbContext.cs), [taxonomy](https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Taxonomy/PaperDotNet.Taxonomy/Data/TaxonomyDbContext.cs), [search](https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Search/PaperDotNet.Search/Data/SearchDbContext.cs), [jobs](https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Jobs/PaperDotNet.Jobs/Data/JobsDbContext.cs), [workflows](https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Workflows/PaperDotNet.Workflows/Data/WorkflowsDbContext.cs), [collaboration](https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Collaboration/PaperDotNet.Collaboration/Data/CollaborationDbContext.cs), and [notifications](https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Notifications/PaperDotNet.Notifications/Data/NotificationsDbContext.cs).

Tasks, notes, calendars, AI workflows, provisioning, and extension configuration add specialized data around the shared list/item model. Their existence is useful evidence for keeping the content core reusable; copying all those application modules would expand our scope substantially. PaperDotNet's [document-module architecture decision](https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/docs/adr/0015-documents-on-the-sdk.md) also emphasizes building document features through extension contracts.

## What to adapt

The order below prioritizes a reusable foundation rather than feature parity.

### 1. Richer fields and reusable content templates

Introduce a field-type registry with definition validation, value normalization, and index encoding. Start with descriptions, defaults, length/range limits, date-only values, email/URL validation, and multi-choice/reference support as needed.

Add reusable schema templates with stable keys before adding unrestricted shared mutable content types. Template adoption should create a new collection SchemaRevision. If we later allow multiple content types within one collection, each item revision should pin its content-type identity/version and fully resolved effective schema. Editing a shared template must not silently reinterpret existing revisions or published content.

PaperDotNet supplies the [field registry contracts](https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Lists/PaperDotNet.Lists.Contracts/Fields.cs) and [template provisioning pattern](https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Lists/PaperDotNet.Lists/Templates/ListTemplates.cs). Its ItemVersion stores ContentTypeId, but the inspected list model does not provide the immutable schema pairing already present in PaperGo.

### 2. Saved views over one bounded query model

ListView supports collection, name, selected columns, filter expression, sort, optional grouping, default flag, and concurrency version. The validated QuerySpec expression tree also powers collection queries and [smart folders](smart-folders.md).

Compile allowed operations into parameterized SQL; retain exact typed comparisons and a stable ID sort tie-breaker. Authorization and head/published selection must occur before pagination, grouping, and counts. Saving a view never grants access to its contents.

PaperDotNet's [ListView and SmartFolder](https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Lists/PaperDotNet.Lists/Data/Entities.cs) demonstrate the capability. An expression-tree API is our proposed Go adaptation; we do not need to adopt its OData wire syntax.

### 3. Transactional outbox and small durable jobs

Write an OutboxEvent in the same transaction as content, publication, grants, relationships, and audit changes. Proposed fields: event ID/type, resource and revision IDs, actor, payload, occurred_at, attempts, next_attempt_at, and lease state. Dispatch after commit with at-least-once delivery and idempotent handlers. Add an Operation record for user-visible asynchronous progress.

PaperDotNet has an [outbox abstraction](https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/BuildingBlocks/PaperDotNet.Messaging/Outbox.cs) and a [central mutation writer](https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Lists/PaperDotNet.Lists/Features/ItemWriter.cs) that commit state and events together. Adopt that invariant, not the entire .NET messaging stack. Synchronous extension hooks must pass through final validation and cannot bypass authorization, revision allocation, projection maintenance, or audit recording.

A durable item change feed can follow this work. It needs explicit rules for access revocation and deletion markers; AuditEvent alone is not an authorization-safe synchronization API.

### 4. Typed, versioned relationships

Add RelationshipType with stable key, display/inverse labels, direction policy, and optional incoming/outgoing cardinality. Add optimistic concurrency and PATCH support to relationship metadata. A bounded attribute schema would be an additional PaperGo design choice: PaperDotNet currently validates a flat scalar property bag.

The reference implements [relationship policies](https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Lists/PaperDotNet.Lists/Features/RelationshipTypes.cs), [attribute bounds](https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Lists/PaperDotNet.Lists/Features/RelationshipAttributes.cs), and [global relationships](https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/docs/adr/0039-global-item-relationships.md). PaperGo should keep both endpoints visible in queries. Requiring write permission on both endpoints, supporting cross-workspace links, and allowing item moves each change existing behavior and deserve explicit policy decisions before implementation.

### 5. Document extraction and deduplicated storage

Add StoredObject for physical content identity/checksum/size/storage key, and keep Blob as immutable item-specific file-version metadata referencing it. Preserve each ItemRevision's exact Blob reference. Add page/text extraction projections and durable extraction jobs before semantic search.

Deduplication must not bypass item authorization, and duplicate warnings must not expose inaccessible documents. Extracted search results must resolve through the currently authorized head/published surface; processing a draft must never make draft text readable to ordinary readers.

PaperDotNet's [StoredFile/FileVersion split and page text](https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Documents/PaperDotNet.Documents/Data/DocumentsDbContext.cs) are directly applicable. Garbage collection needs reference checks, a grace period, and crash reconciliation rather than deleting bytes when one item is removed.

### 6. Governed terms, group principals, and lifecycle operations

Add optional TermSet/Term identifiers when controlled vocabularies become necessary; retain free tags for simple applications. Add group expansion and workspace membership when enterprise authorization needs them, with documented invalidation and revocation behavior.

A recycle/restore lifecycle, explicit retention rules, consistent SQLite/blob backups, and tested restoration also belong in the foundation roadmap. PaperDotNet's soft-delete fields and configurable version retention are useful inputs, but automatic pruning must not remove a retained or published PaperGo revision/schema/blob reference.

## What to preserve or defer

| PaperDotNet approach | Decision for PaperGo |
| --- | --- |
| Nearest unique permission ScopeId and ACL level | Preserve our additive grants through ancestors until an inheritance boundary and separate draft/publication capabilities. Direct substitution changes semantics. |
| Background propagation of large permission-scope changes | Preserve immediate transactional authorization changes. The reference documents stale old-scope access during propagation; do not inherit that revocation delay. |
| Fixed promoted numeric slots using double | Preserve int64 integers and scaled decimal units. Approximate numeric indexes are inappropriate for exact money/integer comparisons. |
| Ten promoted slots per scalar category | Keep flexible typed FieldValue rows initially. Benchmark representative workloads before introducing bounded hot-field columns. |
| Mutable reusable content types and configurable version pruning | Keep frozen effective schemas and immutable retained history. Add explicit schema adoption and retention policy. |
| Multi-tenant platform and PostgreSQL-specific optimizations | Keep the agreed one-organization SQLite deployment. Preserve adapter boundaries for a later measured migration. |
| Complete workflow, identity server, semantic search, and application modules | Defer until demanded by actual applications. Build durable events and stable extension contracts first. |

The permission and promoted-column details come from [the scale architecture decision](https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/docs/adr/0035-item-storage-and-permissions-at-scale.md) and [the actual database mappings](https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Lists/PaperDotNet.Lists/Data/ListsDbContext.cs). Audit change summaries/trace correlation are also useful additions based on [AuditEntry](https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/BuildingBlocks/PaperDotNet.Persistence/AuditEntry.cs), but audit storage and outbound event delivery should remain distinct responsibilities.

## Recommended starting scope

The next implementation slice should combine richer field validation, reusable template provisioning, and saved views over a bounded query specification. These improve many DMS/CMS applications while retaining our existing content model. The next infrastructure slice should add a transactional outbox and operation tracking, making document extraction and notifications reliable. Typed relationships, controlled taxonomy, and deduplicated document storage can then be introduced independently.

No .NET implementation details require changing our choice of Go, Ent, Atlas, or SQLite.

