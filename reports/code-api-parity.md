# PaperGo / PaperDotNet code and API parity audit

**Result: the two apps do not currently cover the same user stories.** PaperGo implements the shared resource, schema, taxonomy and workflow foundations, but many application modules are absent. Existing overlapping features also differ in destructive edits, permission checks, bulk behavior and workflow authority.

LST-05 is covered by live organization content-type and field catalogs with collection attachments (`93d4a73`). LST-31 is covered through nested group ACLs and persisted module roles. TAX-01 and TAX-03 are covered by the organization term store and independent taxonomy scopes (`c0fdd01`). Current implementation results are in [workspace alignment](workspaces-alignment.md) and the [47-story Lists/Taxonomy table](lists-taxonomy-alignment.md). The module inventory below records the pinned audit baseline.

Audit date: 2026-10-10. Baselines: PaperDotNet [`96df412`](https://github.com/eduardpaul/PaperDotNet/tree/96df412f040f53cc14711aaba35880e24817e084) and local PaperGo `8bc2724afaf9e819b0bf99c2b20df5dc36acc363`. PaperGo was clean when the audit began. This report is the only repository change made for the audit.

Evidence comes from registered host modules/routes, request and response types, handlers, service implementations, persistence schemas, activity registries and targeted tests. Project documentation, planning files and OpenAPI documents were not used to establish feature coverage. Generated client code was not treated as implementation evidence.

The inventory covers all 19 product module directories in PaperDotNet plus host/API operations. Smart folders are grouped under Lists, where the .NET code implements them. Search includes its optional Zvec provider. A module with no direct REST routes can still implement functionality: AiWorkflows contributes registered workflow activities.

## Reading the table

“Covered” means code supports the core story described in that row; it does not certify identical wire contracts, UI behavior or every edge case. “Partial” means a narrower equivalent exists. “Missing” means no corresponding registered API/service/model was found. “Drift” means the implemented behavior or scope differs. “Go extra” means an added Go story absent from the inspected .NET baseline. Absence findings were checked against the complete Go router, schema and activity registry, rather than inferred from file names alone.

This is a source and API audit. PaperDotNet was not built or run, and no shared cross-app acceptance harness was executed. External provider-dependent features are explicitly qualified. Use the acceptance scenarios below before claiming behavioral parity. Story counts reflect this audit’s decomposition, not a percentage of product completion.

## Module overview

**130 story comparisons:** 28 covered, 13 partial, 13 drift, 72 missing, 4 go extra. The LST-05, LST-22, LST-25, LST-28, LST-31, LST-33, LST-39, TAX-01 and TAX-03 rows include their current alignment; other baseline rows retain the pinned module audit, with subsequent implementation results in the linked alignment reports.

| Module | Stories | Covered | Partial | Drift | Missing | Go extra | Observed coverage / principal gap |
| --- | --- | --- | --- | --- | --- | --- | --- |
| [Tenancy](#tenancy) | 3 | 0 | 0 | 1 | 2 | 0 | Single-service workspace model; tenant isolation, host resolution and organization API missing. |
| [Identity](#identity) | 10 | 0 | 1 | 1 | 8 | 0 | External token verification exists; local accounts, OAuth issuance, groups, roles, client registry and preferences missing. |
| [Workspaces](#workspaces) | 4 | 1 | 0 | 0 | 3 | 0 | Create/discover/rename covered; descriptions, membership and delete missing. |
| [Lists](#lists) | 42 | 17 | 2 | 7 | 13 | 3 | Core item/schema/query/view/ACL/relations, filtered bulk edits, cross-workspace lookups and collection templates covered; moves, restore and delta incomplete; several write semantics drift. |
| [Taxonomy](#taxonomy) | 5 | 5 | 0 | 0 | 0 | 0 | Organization vocabulary, independent contributor/curator scopes, curation/import/keywords covered. |
| [Documents](#documents) | 13 | 1 | 1 | 1 | 9 | 1 | Raw versioned files covered; OCR, text, previews, pages, inboxes, duplicates and reviews missing; WebDAV added. |
| [Search](#search) | 6 | 0 | 1 | 1 | 4 | 0 | Metadata search exists; body search, snippets, facets, relevance and semantic/hybrid retrieval missing or drifted. |
| [Jobs](#jobs) | 4 | 1 | 2 | 0 | 1 | 0 | Durable runner exists; generic operations, live events and subscriber/mutator extension contracts incomplete. |
| [Workflows](#workflows) | 13 | 3 | 4 | 1 | 5 | 0 | Core graph/run/form APIs covered; approvals, scripts, loops, external waits, trigger families, concurrency and action catalog incomplete. |
| [AiWorkflows](#aiworkflows) | 2 | 0 | 0 | 0 | 2 | 0 | AI activities, provider gateway, batches and usage/budget controls missing. |
| [Collaboration](#collaboration) | 2 | 0 | 0 | 0 | 2 | 0 | Comments, replies, mentions and item activity missing. |
| [Notifications](#notifications) | 5 | 0 | 0 | 0 | 5 | 0 | Inbox, subscriptions, preferences, delivery/digests and outgoing webhook integrations missing. |
| [Tasks](#tasks) | 5 | 0 | 0 | 0 | 5 | 0 | Task-specific schema/assignment, checklist, dependencies, personal views, recurrence and reminders missing. |
| [Calendar](#calendar) | 5 | 0 | 0 | 0 | 5 | 0 | Event/occurrence APIs, agenda, ICS, feeds, source sync and reminders missing. |
| [Notes](#notes) | 2 | 0 | 0 | 0 | 2 | 0 | Note templates, hashtag extraction, wiki links/backlinks and rename rewriting missing. |
| [Provisioning](#provisioning) | 2 | 0 | 1 | 0 | 1 | 0 | Schema/taxonomy/smart-folder packages cover a subset; whole-app provisioning and content portability missing. |
| [ExtensionHost](#extensionhost) | 2 | 0 | 0 | 0 | 2 | 0 | Per-organization activation/configuration and contributed extension SDK/runtime missing. |
| [Mcp](#mcp) | 1 | 0 | 0 | 0 | 1 | 0 | Authenticated MCP transport and tool catalog missing. |
| [Audit](#audit) | 1 | 0 | 1 | 0 | 0 | 0 | Workspace audit exists; organization-wide aggregation and investigation filters incomplete. |
| [API and operations](#api-and-operations) | 3 | 0 | 0 | 1 | 2 | 0 | HTTP batching and administrative backup/restore absent; error/pagination contracts drift. |

## Highest-impact acceptance scenarios

These are proposed parity checks derived from implemented behavior. They are not test results. Rows that involve scope differences require an explicit product decision: reproduce the upstream story or accept a documented scope difference.

| Order | Scenario | Acceptance evidence needed | Affected modules |
| --- | --- | --- | --- |
| 1 | Patch one of two populated fields | Create A and B; PATCH only A using the latest version. B must survive; null must remove A. Ordinary REST and workflow updates must have the chosen consistent semantics. | Lists / PATCH drift |
| 2 | Recover an accidental deletion | Delete a populated folder, observe whether the operation rejects or deletes descendants; then exercise recycle-bin listing, restore and purge. Define the intended result before shipping. | Lists / deletion and recovery |
| 3 | File an Inbox document into another library | Move across libraries and workspaces with destination ACLs/schema; preserve item ID, history, file versions, relations and contributed task/note/calendar state. | Lists + Documents / cross-container move |
| 4 | Create a relationship under asymmetric permissions | With source write and target read only, test link creation, repeated link creation, mutation from target, cross-workspace peers and a type created by label. Compare actual permission and retry outcomes. | Lists + Identity + Taxonomy / graph drift |
| 5 | Apply one content type/vocabulary to two workspaces | Attach one .NET content type and term set to collections in separate workspaces, change its definition, and query old/new items. Decide whether Go should share live definitions or explicitly retain workspace-local adoption. | Lists + Taxonomy / scope drift |
| 6 | Bulk edit a large filtered set with one invalid item | Run an edit over more than 100 matches with one rejected write; inspect matched/updated/failed counts, progress and successful items. Compare that with Go’s explicit all-or-nothing batch. | Lists + Jobs / bulk drift |
| 7 | Approve a generated document replacement | Upload a scan, extract/OCR text, stage a replacement, display the proposed file, collect/escalate a human decision, resume exactly once, then inspect old/new file versions. | Documents + Workflows + Notifications |
| 8 | Use the app as a team member | Sign in, discover me/home, join a workspace via a group, assign a task to an active user, mention them in a comment, and receive/read the notification. | Identity + Workspaces + Tasks + Collaboration |
| 9 | Find content occurring only inside a file | Search an extracted PDF phrase and scan text; inspect relevance, highlighted snippet/page location and access filtering. Exercise semantic mode only with a configured provider. | Documents + Search + AiWorkflows |
| 10 | Edit one occurrence of a repeating event | Create an event in a non-UTC time zone, expand a date range across a DST boundary, move/cancel one occurrence, export/reimport ICS and revoke a private feed. | Calendar |
| 11 | Repeat a task and maintain linked notes | Complete a recurring assigned task and verify next due date/reminder; rename a note linked from another note and verify wiki links/backlinks and inline tags. | Tasks + Notes |
| 12 | Resume/synchronize after restart and permission loss | Run a persisted workflow with a restricted author, revoke rights and restart; compare authority. Exercise external wait completion, concurrency policy, delta tokens and live events. | Workflows + Lists + Jobs |
| 13 | Recreate an application from a portable package | Export configuration and content, apply with parameters/dry-run, reapply without duplicates, and verify files and module state; distinguish this from applying fields to one existing type. | Provisioning |

## Tenancy

Single-service workspace model; tenant isolation, host resolution and organization API missing.

| ID | User story | Status | PaperDotNet code/API | PaperGo code/API | Effect / acceptance gap | Code evidence |
| --- | --- | --- | --- | --- | --- | --- |
| TEN-01 | Host several isolated organizations | Drift | Tenant-owned persistence and tenant context in the registered Tenancy module. | One deployment has workspaces; resources have no tenant boundary. | Multi-organization hosting requires an explicit scope decision; workspace ACLs alone do not provide tenant isolation. | [.NET: PaperDotNetHost.cs:54][N-HOST]<br>[.NET: TenantDirectory.cs:12][N-TENDIR]<br>[Go: resource.go:16][G-RESOURCE]<br>[Go: config.go:11][G-CONFIG] |
| TEN-02 | Resolve an organization by host and administer its lifecycle | Missing | HostMappingStrategy and TenantDirectory; tenant commands create/manage tenants. | No host-to-tenant resolution or tenant directory/CLI. | Separate organizations cannot be provisioned and resolved by host in one service. | [.NET: HostMappingStrategy.cs:7][N-TEN]<br>[.NET: TenantDirectory.cs:12][N-TENDIR]<br>[.NET: AdminCli.cs:16][N-CLI]<br>[Go: main.go:24][G-MAIN] |
| TEN-03 | Read organization metadata | Missing | GET organization returns the current tenant's organization. | No organization API/entity. | Clients cannot discover organization identity/settings through the API. | [.NET: OrganizationEndpoints.cs:20][N-ORG]<br>[Go: api.go:43][G-ROUTES] |

## Identity

External token verification exists; local accounts, OAuth issuance, groups, roles, client registry and preferences missing.

| ID | User story | Status | PaperDotNet code/API | PaperGo code/API | Effect / acceptance gap | Code evidence |
| --- | --- | --- | --- | --- | --- | --- |
| ID-01 | Sign in locally with password/passkey and sign out | Missing | auth/login, auth/logout and passkey login/registration handlers. | Verifies bearer tokens; no accounts, sessions, password or passkey endpoints. | A user needs an external identity service for the production sign-in story. | [.NET: AuthEndpoints.cs:45][N-AUTH]<br>[Go: auth.go:14][G-AUTH]<br>[Go: api.go:43][G-ROUTES] |
| ID-02 | Authorize clients and issue/refresh OAuth tokens | Missing | OAuthEndpoints implements token/authorization flows; applications have management endpoints. | OIDC IDTokenVerifier validates an issuer/audience; it does not issue tokens. | Token consumption covers a different role from acting as the application's authorization server. | [.NET: OAuthEndpoints.cs:34][N-OAUTH]<br>[.NET: ApplicationEndpoints.cs:61][N-APP]<br>[Go: auth.go:14][G-AUTH] |
| ID-03 | Create scoped personal API credentials with expiry/revocation | Partial | me/apiTokens creates/lists/revokes scoped tokens. | webdav-credentials issues caller-owned expiring app passwords for WebDAV. | No personal scoped REST API token creation; WebDAV credentials do not satisfy that story. | [.NET: MeEndpoints.cs:50][N-TOKEN]<br>[Go: credentials.go:36][G-CRED]<br>[Go: api.go:43][G-ROUTES] |
| ID-04 | Administer users and password/account lifecycle | Missing | users list/get/create/update/delete, password reset and own password change handlers. | No user directory or account lifecycle; principals are subject strings. | Cannot discover people, disable an account, anonymize it, or manage its credentials within PaperGo. | [.NET: DirectoryEndpoints.cs:62][N-DIR]<br>[.NET: AccountEndpoints.cs:58][N-ACCOUNT]<br>[Go: auth.go:14][G-AUTH]<br>[Go: resource.go:16][G-RESOURCE] |
| ID-05 | Grant access through groups, including nested groups | Missing | groups/members and groups/groups handlers; group closure and expansion are implemented. | ACL checks match g.subject exactly. | Group grants, nested membership and group revocation do not work as user-directory concepts. | [.NET: DirectoryEndpoints.cs:62][N-DIR]<br>[Go: permissions.go:30][G-ACL] |
| ID-06 | Assign custom roles made from fine-grained scopes | Drift | roles, assignments and scope catalog; endpoints require module scopes plus resource permissions. | Five resource capabilities: read, read_draft, write, publish, manage. | Cannot reproduce module-specific restrictions such as downloading without page editing. | [.NET: DirectoryEndpoints.cs:62][N-DIR]<br>[.NET: DocumentEndpoints.cs:67][N-DOC]<br>[Go: permissions.go:30][G-ACL]<br>[Go: api.go:43][G-ROUTES] |
| ID-07 | Register applications and rotate their secrets | Missing | applications CRUD and secret rotation handlers. | No client/application registry. | Third-party OAuth client onboarding is unavailable. | [.NET: ApplicationEndpoints.cs:61][N-APP]<br>[Go: api.go:43][G-ROUTES] |
| ID-08 | Sign in through a trusted authentication proxy | Missing | OAuthEndpoints maps proxy sign-in; trusted-proxy implementation supports identity headers. | Bearer authentication only, plus Basic app passwords on WebDAV. | Existing proxy SSO cannot authenticate by trusted user/group headers. | [.NET: OAuthEndpoints.cs:34][N-OAUTH]<br>[Go: auth.go:14][G-AUTH]<br>[Go: main.go:24][G-MAIN] |
| ID-09 | Set user preferences and organization defaults | Missing | me/preferences and organization/preferences merge-patch handlers. | No locale/time-zone/theme/document-language preferences API. | Personal defaults and inherited organization settings are absent. | [.NET: PreferencesEndpoints.cs:80][N-PREF]<br>[Go: api.go:43][G-ROUTES]<br>[Go: config.go:11][G-CONFIG] |
| ID-10 | Discover the current user and profile through the API | Missing | GET me returns the current user's profile and identity context. | No me/user-profile route; verified principal is a subject string. | Clients cannot obtain the same signed-in user profile without an external identity service. | [.NET: MeEndpoints.cs:50][N-TOKEN]<br>[Go: auth.go:14][G-AUTH]<br>[Go: api.go:43][G-ROUTES] |

## Workspaces

Create/discover/rename covered; descriptions, membership and delete missing.

| ID | User story | Status | PaperDotNet code/API | PaperGo code/API | Effect / acceptance gap | Code evidence |
| --- | --- | --- | --- | --- | --- | --- |
| WS-01 | Create, discover and rename workspaces | Covered | Workspaces GET/POST/GET-one/PATCH. | Workspace resources POST/GET and resource GET/PATCH. | Basic workspace organization is present; HTTP shapes differ. | [.NET: WorkspaceEndpoints.cs:44][N-WS]<br>[Go: api.go:43][G-ROUTES]<br>[Go: service.go:129][G-SERVICE] |
| WS-02 | Maintain workspace descriptions | Missing | Workspace request/response includes Description. | Workspace resource/create/update has a name and tags, no description. | A workspace's descriptive metadata cannot be preserved. | [.NET: WorkspaceEndpoints.cs:44][N-WS]<br>[Go: service.go:129][G-SERVICE]<br>[Go: service.go:307][G-PATCH] |
| WS-03 | Manage workspace members and roles | Missing | members GET/POST/DELETE with ownership guards. | Grants on workspace resources; no membership entity or role directory. | Membership management and owner/member/reader lifecycle are missing. | [.NET: WorkspaceEndpoints.cs:44][N-WS]<br>[Go: permissions.go:30][G-ACL]<br>[Go: api.go:43][G-ROUTES] |
| WS-04 | Delete a workspace | Missing | Workspace DELETE handler and associated lifecycle. | Resource deletion accepts only folder/item. | Owners cannot remove a workspace through PaperGo. | [.NET: WorkspaceEndpoints.cs:44][N-WS]<br>[Go: service.go:509][G-DELETE] |

## Lists

Core item/schema/query/view/ACL/relations covered; moves, restore, delta and templates incomplete; several write semantics drift.

| ID | User story | Status | PaperDotNet code/API | PaperGo code/API | Effect / acceptance gap | Code evidence |
| --- | --- | --- | --- | --- | --- | --- |
| LST-01 | Create structured lists and file libraries | Covered | Workspace lists route with list/library kind. | Workspace children create list/library resource kinds. | The foundational container story is covered. | [.NET: ListEndpoints.cs:41][N-LIST]<br>[Go: service.go:129][G-SERVICE] |
| LST-02 | Configure description and whether folders are allowed | Missing | List Description and AllowFolders on create/update. | No container description/allow_folders property; folders are supported structurally. | Cannot describe a collection or enforce a flat collection through configuration. | [.NET: ListEndpoints.cs:41][N-LIST]<br>[Go: service.go:129][G-SERVICE]<br>[Go: resource.go:16][G-RESOURCE] |
| LST-03 | Delete a list or library | Missing | List DELETE (with system-list guard). | DELETE rejects list/library kinds. | Managers cannot retire user-created collections through the API. | [.NET: ListEndpoints.cs:41][N-LIST]<br>[Go: service.go:509][G-DELETE] |
| LST-04 | Use several content types in one collection | Covered | ContentTypeIds on list and per-item ContentTypeId. | Organization content types attached by ID, field_ids and per-collection defaults; item content_type_id. | Invoice and Contract items can share a library in both. | [.NET: ListEndpoints.cs:41][N-LIST]<br>[.NET: ItemEndpoints.cs:18][N-ITEM]<br>[Go: contenttypes.go:15][G-CT]<br>[Go: service.go:129][G-SERVICE] |
| LST-05 | Share a live content type across collections | Covered | Tenant contentTypes catalog, attached to lists by ID. | Organization ContentType and FieldDefinition catalogs; field_ids reference shared fields, collection_types attaches the same type ID and keeps defaults per collection. Catalog/type/field/template edits advance every attached effective schema atomically. | Live definitions can span collections and workspaces. Invalid required fields, rules, key collisions, remote views and uniqueness failures roll back all attachments; historical/published snapshots stay frozen. Go retains independent catalog-field IDs. | [.NET: ContentTypeEndpoints.cs:83][N-CT]<br>[.NET: ListEndpoints.cs:41][N-LIST]<br>[Go catalog/attachments](C:/Users/eduapaul/Documents/PaperGo/internal/dms/contenttypes.go)<br>[Shared definition tests](C:/Users/eduapaul/Documents/PaperGo/internal/dms/shared_contenttypes_test.go)<br>[HTTP sharing/scopes](C:/Users/eduapaul/Documents/PaperGo/internal/httpapi/shared_contenttypes_test.go) |
| LST-06 | Change an existing item's content type or detach a list content type | Missing | Item PATCH contentTypeId; list DELETE contentTypes/{id}. | Resource content_type_id is immutable; no content-type detach/delete route. | Cannot reclassify Invoice into Contract or detach an unused type through equivalent APIs. | [.NET: ItemEndpoints.cs:18][N-ITEM]<br>[.NET: ListEndpoints.cs:41][N-LIST]<br>[Go: resource.go:16][G-RESOURCE]<br>[Go: service.go:307][G-PATCH]<br>[Go: foundation.go:69][G-FOUND] |
| LST-07 | Model the shared basic field types with validation/defaults | Covered | Text/note/email/URL/date/dateTime/choice/boolean/lookup/managed metadata/keywords normalizers. | Corresponding Go normalizers plus default/required/bounds/multiplicity options. | Core field modeling exists; individual type contracts need the following drift checks. | [.NET: FieldTypes.cs:12][N-FIELDS]<br>[.NET: ContentTypeEndpoints.cs:83][N-CT]<br>[Go: fieldtypes.go:24][G-FIELDS] |
| LST-08 | Model people as validated field values | Missing | person normalizer checks an active organization's user ID, with multiple values. | No person type in fieldTypes. | A text field cannot reproduce assignee selection and active-user validation. | [.NET: FieldTypes.cs:12][N-FIELDS]<br>[Go: fieldtypes.go:24][G-FIELDS] |
| LST-09 | Model currency and precise numerical values | Partial | currency validates a three-letter CurrencyCode; number/currency use decimal normalization. | decimal/int64 preserve exact values and scale; no currencyCode/type. | Money amounts can be precise, but currency metadata and the same numerical range are not represented. | [.NET: FieldTypes.cs:12][N-FIELDS]<br>[Go: fieldtypes.go:24][G-FIELDS] |
| LST-10 | Apply the same default string/URL validation limits | Drift | Default text max 255, note 100000, URL 2048; configurable text ceiling 4000/note 1000000; length counts UTF-16 units; HTTP(S) URL parser. | No default max; configured max must be 1..65536, counted as Unicode runes; URL rejects credentials. | Identical constraints can accept/reject different lengths, especially non-BMP characters. Some .NET limits cannot be configured identically in Go. | [.NET: FieldTypes.cs:12][N-FIELDS]<br>[Go: fieldtypes.go:24][G-FIELDS] |
| LST-11 | Create/update/read validated items with optimistic concurrency | Covered | Item CRUD uses writer validation and If-Match. | Item resource CRUD validates values and requires versions/If-Match. | Concurrent edits are guarded in both; error status codes differ (see API contracts). | [.NET: ItemEndpoints.cs:18][N-ITEM]<br>[.NET: ItemWriter.cs:31][N-WRITER]<br>[Go: service.go:129][G-SERVICE]<br>[Go: api.go:43][G-ROUTES] |
| LST-12 | Retry item creation safely with a caller-selected identity | Missing | CreateItemRequest accepts Id; a repeated authorized create returns the original item. | CreateResource has no caller ID/idempotency key. | A client retry after a lost response can create a second item. | [.NET: ItemEndpoints.cs:18][N-ITEM]<br>[Go: service.go:129][G-SERVICE] |
| LST-13 | Patch one field while retaining other fields | Drift | PATCH fields merges keys; null removes a value. | PATCH values replaces the entire map; workflow item.update separately merges. | A normal API client must read/merge all values to avoid losing unrelated fields. | [.NET: ItemEndpoints.cs:18][N-ITEM]<br>[Go: service.go:307][G-PATCH]<br>[Go: activities.go:146][G-WFACTION] |
| LST-14 | Create folders and move within a collection | Covered | Items with IsFolder, parentId and children API. | Distinct folder resources and parent_id move. | Physical organization within one list/library is covered. | [.NET: ItemEndpoints.cs:18][N-ITEM]<br>[Go: service.go:129][G-SERVICE]<br>[Go: service.go:446][G-MOVE] |
| LST-15 | Classify folders using content-type fields | Missing | Folders are items and writer supports their custom fields. | Create/update explicitly restricts custom values/content_type_id to items. | Folder keywords/terms/custom metadata cannot be expressed as folder fields. | [.NET: ItemWriter.cs:31][N-WRITER]<br>[Go: service.go:129][G-SERVICE]<br>[Go: service.go:307][G-PATCH] |
| LST-16 | Move an item across lists/libraries/workspaces, retaining identity | Missing | POST items/{id}/move calls MoveAcrossListsAsync and module move participants. | Move validates the same immutable container; workspace/container IDs are immutable. | Cannot file Inbox content into a destination library while preserving identity/history/links. | [.NET: GlobalItemEndpoints.cs:16][N-GLOBAL]<br>[Go: service.go:446][G-MOVE]<br>[Go: resource.go:16][G-RESOURCE]<br>[Go: files_test.go:154][G-FILETEST] |
| LST-17 | Delete a populated folder | Drift | ItemWriter DeleteAsync rejects a nonempty folder. | Folder DELETE tombstones the entire subtree after write checks. | The same delete intent fails in PaperDotNet and removes a subtree in PaperGo. | [.NET: ItemWriter.cs:31][N-WRITER]<br>[Go: service.go:509][G-DELETE] |
| LST-18 | Browse a recycle bin, restore deleted content and purge | Missing | recycleBin GET, POST restore and DELETE purge. | Final hidden tombstones; no restore, recycle-bin browsing or purge endpoint. | Accidental deletion cannot be undone through the product API. | [.NET: ItemHistoryEndpoints.cs:28][N-HISTORY]<br>[Go: service.go:509][G-DELETE]<br>[Go: api.go:43][G-ROUTES] |
| LST-19 | Choose whether history is kept and limit its length | Drift | List Versioning and MaxVersions; writer skips history when off and prunes it. | Every item write creates retained immutable revision/schema records; no collection retention switch. | History-off and bounded-history stories are not covered; Go favors retained auditability. | [.NET: ListEndpoints.cs:41][N-LIST]<br>[.NET: ItemWriter.cs:31][N-WRITER]<br>[Go: revisions.go:119][G-SCHEMA]<br>[Go: resource.go:16][G-RESOURCE] |
| LST-20 | Read history and interpret its original schema | Partial | Item version list/get returns snapshot values, changed fields and content type. | Revision list includes immutable schema and blob references; no single numbered revision route. | History reading exists and Go freezes schema context, but clients lack the same per-version/diff metadata API. | [.NET: ItemHistoryEndpoints.cs:28][N-HISTORY]<br>[Go: revisions.go:119][G-SCHEMA]<br>[Go: api.go:43][G-ROUTES] |
| LST-21 | Restore an earlier metadata version | Missing | POST versions/{number}/restore rewrites old values through the writer. | No revision-restore endpoint. | Reading old values is possible; one operation to restore them is unavailable. | [.NET: ItemHistoryEndpoints.cs:28][N-HISTORY]<br>[Go: api.go:43][G-ROUTES] |
| LST-22 | Bulk edit by filter with operation progress and per-item failures | Covered | bulkUpdate starts a filtered operation, merges fields, returns matched/updated/failed counts. | bulk-update starts a durable typed-query operation. Selection freezes on first execution; each item merges fields through the normal current write path and commits independently. Operations expose matched/processed/updated/failed counts, progress and up to 50 item failure reasons. | Filtered edits cover partial success and restart/resume. Go uses QuerySpec rather than OData, selects readable head content and requires current collection access; publishing remains a separate action. Explicit bulk actions remain atomic. | [.NET: BulkUpdate.cs:17][N-BULK]<br>[Go: bulk.go:10][G-BULK] <br>[Filtered operation](C:/Users/eduapaul/Documents/PaperGo/internal/dms/bulkupdate.go)<br>[Operation regressions](C:/Users/eduapaul/Documents/PaperGo/internal/dms/bulkupdate_test.go)<br>[REST tests](C:/Users/eduapaul/Documents/PaperGo/internal/httpapi/bulkupdate_test.go) |
| LST-23 | Save views with columns, filters, sorting and grouping | Covered | ViewRequest and viewId on item queries. | ListView plus typed QuerySpec; view query/group endpoints project columns. | Saved-query and column-selection stories exist in both. | [.NET: ViewEndpoints.cs:18][N-VIEW]<br>[Go: listview.go:15][G-VIEW]<br>[Go: foundation.go:69][G-FOUND]<br>[Go: queryspec.go:30][G-QUERY] |
| LST-24 | Store a view's table/board/calendar/gallery layout | Covered | ViewRequest has ViewLayout persisted with the definition. | ListView and ViewInput persist and validate layout. The field exists in the pinned Go baseline. | Saved presentation choice is covered; corrected by inspecting the baseline schema and view service. | [.NET: ViewEndpoints.cs:18][N-VIEW]<br>[Go: listview.go:15][G-VIEW] |
| LST-25 | Query items with multiple sort keys and selectable fields | Covered | OData orderBy/select, filters, count, paging and view defaults. | Ordered sort arrays with independent directions, exact typed keyset cursors and ad hoc select on direct or saved-view queries. | Up to eight unique ready scalar sort keys and 50 selected fields; missing values last per key. Projection leaves the Go resource metadata envelope present. Multi-key queries sort the authorized matching set; single-key scans retain their index optimization. | [.NET: ItemEndpoints.cs:18][N-ITEM]<br>[.NET: ItemQueryRunner.cs:59][N-QUERY]<br>[Go query ordering](C:/Users/eduapaul/Documents/PaperGo/internal/dms/queryordering.go)<br>[Sorting/projection tests](C:/Users/eduapaul/Documents/PaperGo/internal/dms/queryordering_test.go) |
| LST-26 | Use relations with direction, cardinality and editable attributes | Covered | Typed edge handlers, direction policy, limits, attribute ETags. | RelationshipType policy, max_incoming/outgoing and attribute schema; PATCH edge metadata. | The structured same-workspace relationship story is present. | [.NET: ItemRelationshipEndpoints.cs:15][N-REL]<br>[Go: relationships.go:16][G-REL]<br>[Go: relationshiptypes.go:12][G-RELT] |
| LST-27 | Link items across workspaces or without a relationship type | Missing | Global relations and typed relationships support tenant-local peers; type is optional. | Link requires a taxonomy predicate and rejects endpoints in different workspaces. | Organization knowledge graphs and quick untyped related-item links are missing. | [.NET: GlobalItemEndpoints.cs:16][N-GLOBAL]<br>[.NET: ItemRelationshipEndpoints.cs:15][N-REL]<br>[Go: relationships.go:16][G-REL] |
| LST-28 | Use taxonomy labels to discover/create relationship types | Covered | Relationship types are term-backed; a type label can be resolved/ensured. | Organization Relationships/Types vocabulary; policy ID equals term ID. Link creation resolves IDs, names, translations and synonyms and contributes unknown labels to the open set. | Labels are curated through taxonomy; compatible merges preserve edges, while conflicting attributes/semantics and used endpoint limits reject curation. Go retains typed attribute schemas, required predicates and within-workspace edges (LST-27). | [.NET: ItemRelationshipEndpoints.cs:15][N-REL]<br>[.NET: RelationshipTypes.cs](https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Lists/PaperDotNet.Lists/Features/RelationshipTypes.cs)<br>[Go predicate vocabulary](C:/Users/eduapaul/Documents/PaperGo/internal/dms/relationshiptypes.go)<br>[Taxonomy edge tests](C:/Users/eduapaul/Documents/PaperGo/internal/dms/relationshipterms_test.go) |
| LST-29 | Apply the same rights/idempotency when creating/removing a link | Drift | Relationship operations use writable endpoints and ensure existing relations; removal can use either endpoint. | Creation needs source write plus target read; duplicate creation conflicts; directed mutation uses the source endpoint. | Same users can create different graphs and retries/removal behave differently. | [.NET: ListItemStore.cs:204][N-RELRULES]<br>[.NET: GlobalItemEndpoints.cs:16][N-GLOBAL]<br>[Go: relationships.go:16][G-REL] |
| LST-30 | Query the workspace relationship graph by edge attributes | Missing | GET workspaces/{id}/relationships accepts type/direction/$filter. | Only per-item incoming/outgoing/type pages; no workspace edge query or single-edge GET. | Cannot query all receipt lines/amount-bearing edges across the workspace in one API request. | [.NET: ItemRelationshipEndpoints.cs:15][N-REL]<br>[Go: api.go:43][G-ROUTES]<br>[Go: relationships.go:16][G-REL] |
| LST-31 | Break/copy/reset inherited permissions | Covered | Scoped ACLs support users, groups and dynamic workspace roles; endpoint operations also require list.read. | Typed user/group/workspace-role ACL principals, nested group expansion, copy/reset, persisted module roles and transactional scope enforcement. | Copies preserve symbolic principals; membership and role revocation take effect immediately. Workspace owners and workspace.manage holders retain ACL control subject to module scopes. The core story is covered; organization-wide catalog boundaries remain separate gaps. | [.NET: PermissionEndpoints.cs:47][N-ACL]<br>[Go: permissions.go:30][G-ACL]<br>[Group/role tests](C:/Users/eduapaul/Documents/PaperGo/internal/dms/grouppermissions_test.go)<br>[Scope catalog](C:/Users/eduapaul/Documents/PaperGo/internal/identity/scopes.go) |
| LST-32 | Discover a personal Home and default Inbox/Documents libraries | Missing | GET me/home provisions/returns personal content locations. | Workspaces must be created explicitly; no personal home/inbox discovery. | An everyday user's initial landing/storage locations are missing. | [.NET: HomeEndpoints.cs:27][N-HOME]<br>[Go: api.go:43][G-ROUTES]<br>[Go: resource.go:16][G-RESOURCE] |
| LST-33 | Create a complete collection from a built-in or extension template | Covered | TemplateKey provisions content types, views and list settings; built-ins register Documents/Contacts, modules add others. | Registered collection templates provision settings, live shared content types and saved views in the create transaction; built-in Documents/Contacts and module registration. | Existing customized template types are reused by stable provisioning identity. Invalid views/schema roll back the whole creation. Go retains immutable history for every item and its publishing/settings contracts. | [.NET: ListEndpoints.cs:41][N-LIST]<br>[.NET: BuiltInTemplates.cs:21][N-TEMPL]<br>[Collection templates](C:/Users/eduapaul/Documents/PaperGo/internal/dms/collectiontemplates.go)<br>[Provisioning tests](C:/Users/eduapaul/Documents/PaperGo/internal/dms/collectiontemplates_test.go) |
| LST-34 | Synchronize changes using a durable delta token | Missing | GET items/delta supports snapshot/delta continuation and deletion/access changes. | Audit feed is managerial; no item delta/change token endpoint. | Offline clients cannot obtain equivalent incremental synchronization. | [.NET: DeltaEndpoints.cs:68][N-DELTA]<br>[Go: api.go:43][G-ROUTES]<br>[Go: audit.go:10][G-AUDIT] |
| LST-35 | Keep drafts separate from published content | Go extra | Inspected item API reads current mutable items; no head/published pointer or publish endpoint. | Auto/head/published surfaces; explicit publish/unpublish with distinct capabilities. | Go introduces an editorial story and visibility semantics that must be an explicit parity exception. | [.NET: ItemEndpoints.cs:18][N-ITEM]<br>[Go: content.go:15][G-PUB]<br>[Go: resource.go:16][G-RESOURCE]<br>[Go: permissions.go:30][G-ACL] |
| LST-36 | Enforce unique business fields and cross-field rules | Go extra | Inspected FieldDefinition/ContentType contract has no Go-style unique/required_if comparison rule definition. | Container business-key claims cover both surfaces; immutable cross-field rules. | Additional validation can reject writes that the .NET model accepts; configure it intentionally. | [.NET: ContentTypeEndpoints.cs:83][N-CT]<br>[Go: contenttypes.go:15][G-CT]<br>[Go: contenttypes_test.go:38][G-TYPETEST] |
| LST-37 | Use personal/shared smart folders across collections with metadata navigation | Covered | SmartFolders query/group/drop/remove endpoints; private/shared definitions. | Personal/shared query definitions, term descendants, 3 grouping levels and cross-collection membership. | Semantic navigation, filing by classification and undoing classification are implemented. | [.NET: SmartFolders.cs:83][N-SMART]<br>[Go: smartfolders.go:24][G-SMART]<br>[Go: smartfolderactions.go:28][G-SMACTION]<br>[Go: smartfolders.go:8][G-SFAPI] |
| LST-38 | Apply portable shared smart-folder definitions | Covered | Smart-folder template handler is registered in ListsModule. | Workspace smart-folders export/import with portable references. | Reusing shared definitions is present; the broader template ecosystem is separate. | [.NET: SmartFolderTemplates.cs:17][N-SMARTT]<br>[Go: smartfoldertemplates.go:42][G-SMPORT]<br>[Go: smartfolders.go:8][G-SFAPI] |
| LST-39 | Reference items in another workspace through a lookup field | Covered | lookupListId requires an existing tenant list; ItemExistsAsync checks target list/item and excludes folders, without a same-workspace or target-reader check. | Lookup definitions target any live organization collection. Assignments/defaults validate live item membership without target read or publication checks; shared types and templates use the same rule. | Cross-workspace catalogs are supported. Target content still requires its own read permission. Explicit assignments revalidate every supplied ID, even an unchanged ID; omitted references survive unrelated edits. | [.NET: ContentTypeEndpoints.cs:83][N-CT]<br>[.NET: FieldTypes.cs:12][N-FIELDS]<br>[.NET: ItemWriter.cs:461][N-LOOKUPVALID]<br>[Go: fieldtypes.go:365][G-LOOKUP] <br>[Lookup regression tests](C:/Users/eduapaul/Documents/PaperGo/internal/dms/lookups_test.go)<br>[HTTP lookup checks](C:/Users/eduapaul/Documents/PaperGo/internal/httpapi/lookups_test.go) |
| LST-40 | Use the same field types as multiple-value arrays | Drift | Choice/person/lookup/managed metadata/keywords support multiple; text/email/URL inherit SupportsMultiple=false. | Multiple allows text/email/URL in addition to choice/lookup/term/keywords; person is absent. | Array-valued text/email/URL schemas accepted by Go are not accepted by the baseline .NET field registry. | [.NET: FieldTypes.cs:12][N-FIELDS]<br>[.NET: Fields.cs:137][N-FIELDBASE]<br>[Go: fieldtypes.go:24][G-FIELDS] |
| LST-41 | Patch one relationship attribute while retaining other attributes | Drift | RelationshipAttributes.Patch merges properties and removes null-valued keys. | UpdateRelationship validates and replaces the entire Metadata map. | A partial edge PATCH drops other attributes in Go; clients need explicit whole-map merging. | [.NET: RelationshipAttributes.cs:14][N-ATTR]<br>[.NET: ListItemStore.cs:204][N-RELRULES]<br>[Go: relationshiptypes.go:185][G-ATTRPATCH] |
| LST-42 | Validate relationship attributes against a declared typed schema | Go extra | Relationship attribute bags permit up to 64 scalar keys/16 KiB; relationship types have no attribute-schema contract. | RelationshipType.Attributes declares field definitions; normalizeMetadata enforces those definitions and their required/type/bound rules. | Go can model stricter business edges, but an upstream arbitrary attribute bag may fail Go validation. | [.NET: RelationshipAttributes.cs:14][N-ATTR]<br>[.NET: ItemRelationshipEndpoints.cs:15][N-REL]<br>[Go: relationshiptypes.go:12][G-RELT]<br>[Go: relationshiptypes.go:185][G-ATTRPATCH] |

## Taxonomy

Organization vocabulary, independent contributor/curator scopes, curation/import/keywords covered.

| ID | User story | Status | PaperDotNet code/API | PaperGo code/API | Effect / acceptance gap | Code evidence |
| --- | --- | --- | --- | --- | --- | --- |
| TAX-01 | Share one governed vocabulary across the organization | Covered | Tenant termStore groups/sets/terms, available across workspace lists. | Organization-owned groups/sets/terms and one shared keywords set; term fields in any workspace reference the same IDs. | Shared IDs resolve in fields, queries, smart folders, portable packages and workflow pickers. Workspace deletion retains the organization vocabulary. | [.NET: TermStoreEndpoints.cs:104][N-TAX]<br>[Go: taxonomy.go:50][G-TAX]<br>[organization_taxonomy_test.go](C:/Users/eduapaul/Documents/PaperGo/internal/dms/organization_taxonomy_test.go:137) |
| TAX-02 | Curate hierarchical terms with labels, synonyms, colors, deprecation, move and merge | Covered | Term store handlers and persisted hierarchy/merge state. | Term APIs and MoveTerm/MergeTerm preserve managed IDs and paths. | Principal vocabulary curation stories are covered. | [.NET: TermStoreEndpoints.cs:104][N-TAX]<br>[.NET: TermStoreEndpoints.Terms.cs:99][N-TAXTERMS]<br>[Go: taxonomy.go:50][G-TAX]<br>[Go: taxonomy.go:13][G-TAXAPI] |
| TAX-03 | Allow contributors to add terms in open sets | Covered | CreateTerm allows taxonomy.read in an open set; closed set needs taxonomy.manage. | taxonomy.read permits open-set terms and keywords independently of workspace membership; taxonomy.manage gates curation and closed-set creation. Persisted user/group role revocation applies immediately. | Taxonomy-only contributors and curators work without content access. Item-field auto-contributions also require taxonomy.read. | [.NET: TermStoreEndpoints.Terms.cs:99][N-TAXTERMS]<br>[taxonomy.go](C:/Users/eduapaul/Documents/PaperGo/internal/dms/taxonomy.go:437)<br>[organization_taxonomy_test.go](C:/Users/eduapaul/Documents/PaperGo/internal/dms/organization_taxonomy_test.go:30) |
| TAX-04 | Add/suggest keywords and promote popular keywords into managed terms | Covered | Keyword creation/suggestions/popular/promote handlers. | Organization keywords, cross-workspace popular counts and promote API; free resource tags are an additional system. | Controlled keywords/promotion exist. Free tags and term-valued keywords need deliberate client mapping. | [.NET: TermStoreEndpoints.cs:104][N-TAX]<br>[Go: taxonomy.go:13][G-TAXAPI]<br>[Go: keywords.go:20][G-KEYWORDS] |
| TAX-05 | Import a governed taxonomy and resolve labels/IDs | Covered | Term store CSV import and term ID lookup. | CSV group import, taxonomy package import/export, IDs/labels/synonyms normalization. | Seed/import and term lookup stories are covered with different package contracts. | [.NET: TermStoreEndpoints.cs:104][N-TAX]<br>[.NET: TermStoreEndpoints.Terms.cs:99][N-TAXTERMS]<br>[Go: termimport.go:40][G-IMPORT]<br>[Go: termexport.go:58][G-TAXPORT]<br>[Go: termvalues.go:32][G-TERMVALUE] |

## Documents

Raw versioned files covered; OCR, text, previews, pages, inboxes, duplicates and reviews missing; WebDAV added.

| ID | User story | Status | PaperDotNet code/API | PaperGo code/API | Effect / acceptance gap | Code evidence |
| --- | --- | --- | --- | --- | --- | --- |
| DOC-01 | Upload/replace/download versioned library file content | Covered | Library documents upload; item file GET/PUT and version listing/download. | Create library item then PUT/GET items/{id}/content; immutable blobs pinned to revisions. | Basic file storage/versioned download is covered with different request sequences. | [.NET: DocumentEndpoints.cs:67][N-DOC]<br>[Go: api.go:472][G-FILE]<br>[Go: api.go:43][G-ROUTES]<br>[Go: revisions.go:119][G-SCHEMA] |
| DOC-02 | Create a document and its metadata from one upload request | Partial | POST lists/{id}/documents accepts multipart upload and creates the item. | REST item creation and raw-byte upload are separate mutations; WebDAV PUT can create a file. | A failed REST upload can leave a metadata-only item; no equivalent multipart metadata/file endpoint. | [.NET: DocumentEndpoints.cs:67][N-DOC]<br>[Go: api.go:472][G-FILE]<br>[Go: api.go:43][G-ROUTES]<br>[Go: webdav.go:28][G-DAV] |
| DOC-03 | Detect/restrict supported file types by content | Drift | FileTypes detects PDF/TIFF/JPEG/PNG/WebP signatures; upload rejects unsupported types. | Generic blob upload accepts the caller's media type and validates its syntax, without signature detection. | Spoofed extensions/media types and arbitrary binary files have different acceptance behavior. | [.NET: FileIntake.cs:9][N-INTAKE]<br>[.NET: DocumentEndpoints.cs:67][N-DOC]<br>[Go: api.go:472][G-FILE]<br>[Go: local.go:54][G-STORE] |
| DOC-04 | Warn/reject duplicate documents by configured library policy | Missing | DuplicatePolicy in document settings and duplicate handling on intake/upload. | No duplicate policy or existing-document warning in the upload API. | Users cannot prevent/triage duplicate ingestion through library settings. | [.NET: DocumentEndpoints.cs:67][N-DOC]<br>[.NET: FileIntake.cs:9][N-INTAKE]<br>[Go: api.go:472][G-FILE]<br>[Go: resource.go:16][G-RESOURCE] |
| DOC-05 | Store identical bytes only once and collect unused objects | Missing | FileIntake reuses StoredFile by hash; StorageCleanup checks unreferenced files. | Local.Put generates a new UUID storage key per upload; no referenced-object garbage collector is wired. | Identical uploads consume new objects; abandoned objects lack the equivalent cleanup lifecycle. | [.NET: FileIntake.cs:9][N-INTAKE]<br>[.NET: StorageCleanup.cs:11][N-GC]<br>[Go: local.go:54][G-STORE]<br>[Go: main.go:24][G-MAIN] |
| DOC-06 | Restore an earlier file version | Missing | POST file/versions/{number}/restore. | Historical blob download is supported for draft readers; no file-restore action. | Cannot promote an older file version in one API operation. | [.NET: DocumentEndpoints.cs:67][N-DOC]<br>[Go: api.go:43][G-ROUTES] |
| DOC-07 | Extract page text and OCR scans into searchable PDFs | Missing | Document workflow activities readText/ocr; registered per-library built-ins and OCR operations. | No extraction/OCR activities or stored page text. | Scanned documents cannot become searchable text through PaperGo. | [.NET: DocumentWorkflows.cs:50][N-DOCWF]<br>[Go: activities.go:146][G-WFACTION]<br>[Go: local.go:54][G-STORE] |
| DOC-08 | Retrieve thumbnails/page images and rerun processing | Missing | file/thumbnail and pages/{page}/image; processing workflows can be started again. | No derivative image endpoints or document processing built-ins. | Preview and processing retry/status stories are missing. | [.NET: DocumentEndpoints.cs:67][N-DOC]<br>[.NET: DocumentWorkflows.cs:50][N-DOCWF]<br>[Go: workflows.go:12][G-WFAPI]<br>[Go: builtins.go:47][G-WFBUILT] |
| DOC-09 | Rotate/reorder/delete/extract/move pages while preserving history | Missing | PUT file/pages and POST pages/extract, pages/move create new file versions. | Only opaque file replacement. | Users must edit documents outside the app; server page manipulation is unavailable. | [.NET: PageOperations.cs:49][N-PAGES]<br>[Go: api.go:43][G-ROUTES] |
| DOC-10 | Send new documents to a personal or group Inbox | Missing | Personal Inbox upload plus groups/{id}/inbox CRUD/upload and me/inboxes. | No personal/group provisioning, membership or inbox endpoints. | Shared intake and personal triage workflows cannot be reproduced. | [.NET: DocumentEndpoints.cs:67][N-DOC]<br>[.NET: GroupInboxEndpoints.cs:36][N-INBOX]<br>[.NET: HomeEndpoints.cs:27][N-HOME]<br>[Go: api.go:43][G-ROUTES] |
| DOC-11 | Configure OCR languages/defaults per file and library | Missing | Upload languages and documentSettings, consumed by document processing. | No OCR/language options on blobs or libraries. | Multilingual processing and inherited OCR defaults are absent. | [.NET: DocumentEndpoints.cs:67][N-DOC]<br>[.NET: DocumentWorkflows.cs:50][N-DOCWF]<br>[.NET: PreferencesEndpoints.cs:80][N-PREF]<br>[Go: resource.go:16][G-RESOURCE]<br>[Go: api.go:472][G-FILE] |
| DOC-12 | Review a transformed file candidate before replacing the current version | Missing | Documents exposes candidate replacement through SDK file contracts; Workflows review/content endpoints deliver the proposed file for a decision. | No file-candidate storage or review/approval endpoints. | A human review of a generated replacement cannot be implemented with the same lifecycle. | [.NET: WorkflowEndpoints.cs:111][N-WF]<br>[.NET: DocumentFileStore.cs:57][N-CANDIDATE]<br>[Go: workflows.go:12][G-WFAPI]<br>[Go: api.go:43][G-ROUTES] |
| DOC-13 | Mount a library in a desktop file client via WebDAV | Go extra | No WebDAV module, handler or host registration found in the inspected source. | Library-scoped WebDAV handles GET/PUT, MKCOL, COPY/MOVE, PROPFIND/PROPPATCH and LOCK/UNLOCK; bearer or app-password authentication. | Go adds a desktop file-client story; include it as a deliberate extension rather than counting it as upstream parity. | [.NET: PaperDotNetHost.cs:54][N-HOST]<br>[Go: webdav.go:28][G-DAV]<br>[Go: methods.go:45][G-DAVMETHOD]<br>[Go: credentials.go:36][G-CRED] |

## Search

Metadata search exists; body search, snippets, facets, relevance and semantic/hybrid retrieval missing or drifted.

| ID | User story | Status | PaperDotNet code/API | PaperGo code/API | Effect / acceptance gap | Code evidence |
| --- | --- | --- | --- | --- | --- | --- |
| SRC-01 | Search metadata across accessible content | Partial | Unified search endpoint across visible organization sources. | Workspace/parent-scoped resource FTS and typed collection/smart-folder queries. | Metadata search exists; one organization-wide search response across all source types does not. | [.NET: SearchEndpoints.cs:26][N-SEARCH]<br>[Go: query.go:43][G-BROWSE]<br>[Go: queryspec.go:30][G-QUERY] |
| SRC-02 | Search extracted document contents | Missing | Document text/pages supply search passages and indexed body. | FTS is populated from resource/revision metadata; no extraction pipeline. | Words occurring only inside PDF/image contents cannot be found. | [.NET: SearchEndpoints.cs:26][N-SEARCH]<br>[.NET: DocumentWorkflows.cs:50][N-DOCWF]<br>[Go: query.go:43][G-BROWSE]<br>[Go: local.go:54][G-STORE] |
| SRC-03 | Use word/phrase/OR/exclusion/prefix syntax and relevance ranking | Drift | SearchService parses expressions and returns Rank. | ftsPhrase quotes the whole query; browse orders IDs, typed query uses its chosen scalar sort. | A query such as invoice OR receipt means different things and result order is not relevance. | [.NET: SearchEndpoints.cs:26][N-SEARCH]<br>[Go: query.go:43][G-BROWSE]<br>[Go: queryspec.go:30][G-QUERY] |
| SRC-04 | Return highlighted snippets, page hits and facets | Missing | SearchHit snippet/page/matchedBy/rank plus SearchFacets. | Resource pages and query groups; no search-specific snippets/pages/facets response. | Metadata grouping does not replace ranked document/page search navigation. | [.NET: SearchEndpoints.cs:26][N-SEARCH]<br>[Go: query.go:43][G-BROWSE]<br>[Go: queryspec.go:30][G-QUERY] |
| SRC-05 | Search semantically or combine keyword and semantic results | Missing | Semantic passages/embeddings and keyword/semantic/hybrid modes (requires configured model). | No vector, embedding or hybrid search implementation. | Meaning-based retrieval is missing; .NET support is configuration-dependent. | [.NET: SearchEndpoints.cs:26][N-SEARCH]<br>[.NET: Semantic.cs:15][N-SEM]<br>[Go: query.go:43][G-BROWSE] |
| SRC-06 | Rebuild the search index as a tracked operation | Missing | POST search/reindex uses sources and operation progress. | Background field_index builds exist; no full search-source reindex API. | Administrators cannot run the same repair/rebuild workflow. | [.NET: SearchEndpoints.cs:26][N-SEARCH]<br>[Go: operations.go:23][G-OPS]<br>[Go: foundation.go:69][G-FOUND] |

## Jobs

Durable runner exists; generic operations, live events and subscriber/mutator extension contracts incomplete.

| ID | User story | Status | PaperDotNet code/API | PaperGo code/API | Effect / acceptance gap | Code evidence |
| --- | --- | --- | --- | --- | --- | --- |
| JOB-01 | Recover durable work after process restart | Covered | Operations, recurring scheduler and workflow interpreter store state/leases. | DBOS runner with durable event dispatch; persisted index-build operations. | Durability infrastructure exists in both; individual job capabilities still differ. | [.NET: JobsModule.cs:12][N-JOBS]<br>[Go: runner.go:42][G-RUNNER]<br>[Go: operations.go:23][G-OPS]<br>[Go: service.go:55][G-TX] |
| JOB-02 | Track asynchronous operations beyond field indexing | Partial | IOperations supports bulk/OCR/import/export/reindex with result/error/progress. | Operations endpoints serve field-index building status; workflows have a separate run model. | Generic operation infrastructure and the advertised operation families are missing. | [.NET: JobsModule.cs:12][N-JOBS]<br>[.NET: Operations.cs:174][N-OPS]<br>[Go: operations.go:23][G-OPS]<br>[Go: foundation.go:69][G-FOUND] |
| JOB-03 | Subscribe to live item/operation/run changes | Missing | GET me/events streams server-sent live item/operation/workflow updates. | No SSE/live stream route. | Clients must poll and cannot receive equivalent live updates. | [.NET: Operations.cs:174][N-OPS]<br>[Go: api.go:43][G-ROUTES] |
| JOB-04 | Register ordered pre-save mutators and reliable background subscribers | Partial | ItemWriter runs registered IItemMutator instances; ListsModule registers event subscribers through transactional messaging. | Central validation/Write/Emit APIs and durable workflow dispatch; no registered mutator/subscriber catalog. | Custom synchronous cancellation/transformation and general subscriber extension stories are absent. | [.NET: ItemWriter.cs:31][N-WRITER]<br>[.NET: ListsModule.cs:91][N-LISTMOD]<br>[Go: extension.go:14][G-EXT]<br>[Go: service.go:55][G-TX]<br>[Go: events.go:14][G-EVENT] |

## Workflows

Core graph/run/form APIs covered; approvals, scripts, loops, external waits, trigger families, concurrency and action catalog incomplete.

| ID | User story | Status | PaperDotNet code/API | PaperGo code/API | Effect / acceptance gap | Code evidence |
| --- | --- | --- | --- | --- | --- | --- |
| WF-01 | Author versioned graph workflows with manual/item/cron triggers | Covered | Workflow CRUD, activity/trigger catalogs, graph definitions, manual runs and immutable versions. | Workflow CRUD/history/catalog; graph, variables, manual/cron/item triggers. | Core configurable automation exists, with different JSON contracts and trigger sets. | [.NET: WorkflowEndpoints.cs:111][N-WF]<br>[.NET: Definitions.cs:20][N-SPEC]<br>[Go: workflows.go:12][G-WFAPI]<br>[Go: spec.go:44][G-WFSPEC] |
| WF-02 | Inspect/cancel/retry durable runs with step outcomes | Covered | Runs list/get/cancel/retry and interpreter state/log. | Workflow-runs list/get/cancel/retry; step outputs/errors retained. | Core run observability and retries exist. | [.NET: WorkflowEndpoints.cs:111][N-WF]<br>[.NET: Runs.cs:583][N-RUN]<br>[Go: workflows.go:12][G-WFAPI]<br>[Go: runner.go:42][G-RUNNER] |
| WF-03 | Trigger on a date field, module events or incoming webhook | Partial | Date offset triggers, document.added/task.completed/comment.added/approval.decided; authenticated webhook handler. | Item create/update/publish/unpublish/delete, manual/schedule, term.merged and wf.* events. | Cron is available; date-per-item deduplication, module trigger families and webhook ingress are missing. | [.NET: Definitions.cs:20][N-SPEC]<br>[.NET: WorkflowEndpoints.cs:111][N-WF]<br>[.NET: TasksModule.cs:38][N-TASKMOD]<br>[.NET: DocumentWorkflows.cs:50][N-DOCWF]<br>[Go: workflows.go:12][G-WFAPI]<br>[Go: spec.go:44][G-WFSPEC] |
| WF-04 | Filter triggers by changed fields, event payload and before/after values | Missing | ChangedFields, Data and WorkflowTriggerParameters.When compare event context. | Trigger filters include terms/term_change/content type and current item condition, without changed-fields/data predicates. | Cannot express 'only when status changes from X to Y' with the same trigger contract. | [.NET: Definitions.cs:20][N-SPEC]<br>[Go: spec.go:44][G-WFSPEC]<br>[Go: events.go:14][G-EVENT] |
| WF-05 | Wait for external completion, then resume once | Partial | WorkflowBookmarks persists waits/early completions and timeout behavior. | delay activity stores a time-based wait; no general external bookmark/resume contract. | Timed waiting exists; approval/payment/external-job callbacks cannot resume equivalent waits. | [.NET: Bookmarks.cs:17][N-BOOK]<br>[.NET: Runs.cs:583][N-RUN]<br>[Go: activities.go:146][G-WFACTION]<br>[Go: workflows.go:12][G-WFAPI] |
| WF-06 | Request approval/input, escalate and decide with a review package | Missing | me/approvals, decision/review/content APIs and approval flow activity. | Launch JSON Schema forms exist; no approval entities/activity/decision API. | A launch form does not cover collecting a human decision during an active process. | [.NET: WorkflowEndpoints.cs:111][N-WF]<br>[.NET: Bookmarks.cs:17][N-BOOK]<br>[Go: forms.go:79][G-WFFORM]<br>[Go: activities.go:146][G-WFACTION] |
| WF-07 | Iterate an array or query result using resumable forEach | Missing | forEach validated and interpreted with loop state. | Bounded graph nodes/cycles, but no forEach activity or array/query loop state. | Cannot declaratively create receipt lines for every extracted line with the same semantics. | [.NET: Definitions.cs:20][N-SPEC]<br>[.NET: Runs.cs:583][N-RUN]<br>[Go: spec.go:44][G-WFSPEC]<br>[Go: activities.go:146][G-WFACTION] |
| WF-08 | Run sandboxed scripts with resumable planned writes | Missing | ScriptRunner and script flow node implement bounded JavaScript and planned item operations. | No script activity/runtime. | Computed mappings and script-based multi-item business logic are absent. | [.NET: Scripts.cs:47][N-SCRIPT]<br>[.NET: Runs.cs:583][N-RUN]<br>[Go: activities.go:146][G-WFACTION] |
| WF-09 | Choose skip/replace/parallel concurrency per workflow/item | Missing | RunConcurrency policies are validated and used by WorkflowStarter. | No concurrency policy in Definition; worker concurrency is a runner-level setting. | A duplicate trigger cannot be configured to skip or replace an existing business process. | [.NET: Definitions.cs:20][N-SPEC]<br>[.NET: Runs.cs:583][N-RUN]<br>[Go: spec.go:44][G-WFSPEC]<br>[Go: runner.go:42][G-RUNNER] |
| WF-10 | Select the run's authority and organization/workspace scope | Drift | Inspected built-in item actions call AsSystem; definitions accept organization/workspace scope and retain StartedBy. | Actions authorize as the pinned version's author; workflow is workspace-bound. | The same automation can succeed/fail under different permissions. StartedBy is not proof that .NET actions run with caller ACLs. | [.NET: Actions.cs:105][N-ACTIONS]<br>[.NET: Runs.cs:583][N-RUN]<br>[.NET: Definitions.cs:20][N-SPEC]<br>[Go: run.go:52][G-WFRUN]<br>[Go: spec.go:44][G-WFSPEC] |
| WF-11 | Move/file items and create tasks/send notifications as activities | Partial | item.file ensures a folder path and renames/moves; task.create and notify registered by modules. | item get/update/create/query/delete/publish/unpublish and term checks; no file-path/task/notify activity. | Generic item writes exist, but common ready-made business actions are missing. | [.NET: Actions.cs:105][N-ACTIONS]<br>[.NET: TasksModule.cs:38][N-TASKMOD]<br>[.NET: NotificationsModule.cs:49][N-NTFMOD]<br>[Go: activities.go:146][G-WFACTION] |
| WF-12 | Enable/configure/copy product built-in workflows | Partial | Built-ins include approval, document text/images/OCR, classification/extraction, AI batch and calendar-source refresh. | Built-in framework exists; current catalog registers items.expire only. | The configuration mechanism is covered; almost all built-in process content is absent. | [.NET: WorkflowsModule.cs:73][N-WFMOD]<br>[.NET: DocumentWorkflows.cs:50][N-DOCWF]<br>[.NET: AiWorkflowsModule.cs:28][N-AI]<br>[.NET: CalendarModule.cs:56][N-CALMOD]<br>[Go: builtins.go:47][G-WFBUILT]<br>[Go: workflows.go:12][G-WFAPI] |
| WF-13 | Launch with typed domain inputs and selected items | Covered | Manual runs accept schema-described inputs and up to 100 item IDs. | Launch InputSchema/domain pickers and per_item or selection runs. | Core forms and selected-item starts exist; allowed domain values differ because Go has no user directory. | [.NET: WorkflowEndpoints.cs:111][N-WF]<br>[.NET: Definitions.cs:20][N-SPEC]<br>[Go: forms.go:79][G-WFFORM]<br>[Go: spec.go:44][G-WFSPEC] |

## AiWorkflows

AI activities, provider gateway, batches and usage/budget controls missing.

| ID | User story | Status | PaperDotNet code/API | PaperGo code/API | Effect / acceptance gap | Code evidence |
| --- | --- | --- | --- | --- | --- | --- |
| AI-01 | Extract/classify/summarize documents and use custom prompts | Missing | Registers ai.extract, ai.classify, ai.summarize and ai.prompt activities; gateway consumes text and supported image inputs. | No AI activities or model gateway registered. | AI-assisted classification/extraction stories are absent; this module has no direct REST routes but is implemented. | [.NET: AiWorkflowsModule.cs:28][N-AI]<br>[.NET: AiActivities.cs:97][N-AIGATE]<br>[Go: activities.go:146][G-WFACTION] |
| AI-02 | Run resumable AI batches with cache, budget and usage accounting | Missing | ai.batch, gateway cache/budget/token usage and durable batch polling/bookmark integration; needs provider configuration. | No AI batch or usage persistence. | Cannot port batch jobs or cost-control behavior. | [.NET: AiBatch.cs:11][N-AIBATCH]<br>[.NET: AiActivities.cs:97][N-AIGATE]<br>[Go: activities.go:146][G-WFACTION] |

## Collaboration

Comments, replies, mentions and item activity missing.

| ID | User story | Status | PaperDotNet code/API | PaperGo code/API | Effect / acceptance gap | Code evidence |
| --- | --- | --- | --- | --- | --- | --- |
| COL-01 | Discuss an item in comments and replies | Missing | GET/POST comments; GET/PATCH/DELETE a comment; one-level replies, mentions, author edit and manager delete; ETag checks. | No comment routes or persistence. | A team cannot discuss an item or notify a mentioned colleague. | [.NET: CommentEndpoints.cs:51][N-COMMENT]<br>[Go: api.go:43][G-ROUTES]<br>[Go: resource.go:16][G-RESOURCE] |
| COL-02 | Read an item's activity timeline | Missing | GET item/activity; indexed actor, summary, changed fields; collaboration events and item-change subscriber. | Workspace audit is an administrative feed; no item activity API. | No user-facing timeline combining changes and discussion. | [.NET: CommentEndpoints.cs:51][N-COMMENT]<br>[.NET: Activity.cs:11][N-ACTIVITY]<br>[Go: audit.go:10][G-AUDIT] |

## Notifications

Inbox, subscriptions, preferences, delivery/digests and outgoing webhook integrations missing.

| ID | User story | Status | PaperDotNet code/API | PaperGo code/API | Effect / acceptance gap | Code evidence |
| --- | --- | --- | --- | --- | --- | --- |
| NTF-01 | Read and manage my notification inbox | Missing | GET me/notifications, unread count, mark one/all read and delete. | No notification inbox, read state or user delivery store. | Approval requests, mentions and reminders cannot reach a personal in-app inbox. | [.NET: NotificationEndpoints.cs:55][N-NOTIFY]<br>[Go: api.go:43][G-ROUTES] |
| NTF-02 | Choose delivery channels, quiet hours and digest time | Missing | User settings select channels, quiet hours and local digest hour; webhook URL, secret rotation and test delivery. | No preferences or notification delivery API. | Cannot control when and how alerts arrive. | [.NET: NotificationEndpoints.cs:55][N-NOTIFY]<br>[.NET: Delivery.cs:18][N-DELIVERY]<br>[Go: api.go:43][G-ROUTES] |
| NTF-03 | Follow a list or item for immediate/daily change alerts | Missing | GET/POST/DELETE me/subscriptions; alert subscriber and recurring digest jobs. | Item outbox drives internal workflow dispatch; no user follow subscriptions. | Internal workflow events do not cover user follow/digest stories. | [.NET: NotificationEndpoints.cs:55][N-NOTIFY]<br>[.NET: Alerts.cs:19][N-ALERTS]<br>[.NET: NotificationsModule.cs:49][N-NTFMOD]<br>[Go: events.go:14][G-EVENT]<br>[Go: runner.go:42][G-RUNNER] |
| NTF-04 | Deliver filtered changes to an external webhook | Missing | Change-subscription CRUD with event/filter settings; durable deliveries, signing and retry. | No change subscriptions or outbound webhook worker. | External integrations cannot subscribe to changes. | [.NET: ChangeNotifications.cs:67][N-CHANGE]<br>[.NET: Delivery.cs:18][N-DELIVERY]<br>[Go: api.go:43][G-ROUTES] |
| NTF-05 | Send a notification from a workflow | Missing | Registered notify activity resolves recipients and sends via INotifications. | No notify activity in built-in activity registry. | Notification steps cannot be ported. | [.NET: NotifyActivity.cs:11][N-NOTIFYACT]<br>[Go: activities.go:146][G-WFACTION] |

## Tasks

Task-specific schema/assignment, checklist, dependencies, personal views, recurrence and reminders missing.

| ID | User story | Status | PaperDotNet code/API | PaperGo code/API | Effect / acceptance gap | Code evidence |
| --- | --- | --- | --- | --- | --- | --- |
| TSK-01 | Create and assign tasks using the task schema and views | Missing | Registered TaskTemplates with task fields/person assignment and list template. | Generic lists can hold some analogous fields; no registered task template/person field/task mutator. | Representing data is insufficient for task assignment behavior. | [.NET: TasksModule.cs:38][N-TASKMOD]<br>[.NET: TaskTemplates.cs:6][N-TASKTEMPL]<br>[Go: fieldtypes.go:24][G-FIELDS]<br>[Go: templates.go:14][G-TEMPL] |
| TSK-02 | Maintain ordered task checklists | Missing | GET/PUT checklist, up to 200 validated entries, ordered done state. | No checklist routes or task tables. | Checklist behavior needs a dedicated implementation. | [.NET: TaskEndpoints.cs:55][N-TASK]<br>[Go: api.go:43][G-ROUTES] |
| TSK-03 | Organize subtasks and blocking dependencies | Missing | GET/POST/DELETE task links; one parent, cycle rejection, blockedBy/blocking and document links. | Generic relationship edges lack task link kinds and dependency/parent constraints. | Generic edges cannot enforce the task graph story. | [.NET: TaskEndpoints.cs:55][N-TASK]<br>[Go: relationships.go:16][G-REL] |
| TSK-04 | See my tasks across lists and create tasks from documents | Missing | GET me/tasks; GET/POST document/tasks links creation to task lists. | Only workspace queries; no assignee-aware personal task aggregation or document-to-task operation. | Cannot port a personal task dashboard or document follow-up. | [.NET: TaskEndpoints.cs:55][N-TASK]<br>[Go: api.go:43][G-ROUTES] |
| TSK-05 | Repeat tasks, remind assignees and trigger completion workflows | Missing | Task RRULE CRUD; recurrence subscriber creates next occurrence; reminder jobs and task.create/task.completed integration. | Generic schedule workflows exist; no task recurrence/completion semantics or reminders. | A schedule trigger alone does not reproduce a repeating task lifecycle. | [.NET: TaskEndpoints.cs:55][N-TASK]<br>[.NET: Recurrences.cs:14][N-TASKREC]<br>[.NET: TasksModule.cs:38][N-TASKMOD]<br>[Go: spec.go:44][G-WFSPEC] |

## Calendar

Event/occurrence APIs, agenda, ICS, feeds, source sync and reminders missing.

| ID | User story | Status | PaperDotNet code/API | PaperGo code/API | Effect / acceptance gap | Code evidence |
| --- | --- | --- | --- | --- | --- | --- |
| CAL-01 | Create validated events and view a date range or personal agenda | Missing | Event template/mutator validates event fields; GET list/calendar and me/calendar returns occurrence-aware ranges. | Generic date fields and range filters; no event type or occurrence/agenda service. | Cannot reproduce agenda/calendar stories with raw date filtering. | [.NET: CalendarEndpoints.cs:50][N-CAL]<br>[.NET: CalendarTemplates.cs:7][N-CALTEMPL]<br>[Go: fieldtypes.go:24][G-FIELDS]<br>[Go: queryspec.go:30][G-QUERY] |
| CAL-02 | Repeat an event and move/cancel one occurrence | Missing | Series RRULE and IANA time zone; occurrence overrides and cancellations. | No series or occurrence data/API. | Recurring calendar edits are absent. | [.NET: CalendarEndpoints.cs:50][N-CAL]<br>[Go: api.go:43][G-ROUTES] |
| CAL-03 | Import/export iCalendar and issue revocable private feeds | Missing | POST calendar/import; GET calendar.ics; me/calendarFeeds CRUD and token-authenticated ICS GET. | No ICS parser/exporter/feed routes. | Calendar client exchange and private subscriptions are absent. | [.NET: CalendarEndpoints.cs:50][N-CAL]<br>[.NET: ICalendar.cs:20][N-ICAL]<br>[Go: api.go:43][G-ROUTES] |
| CAL-04 | Subscribe to a remote calendar and refresh replicated events | Missing | Calendar-source CRUD, refresh operation, polling/workflow and source-managed replicated items. | No calendar source registry, fetch job or replication. | Cannot keep external calendars synchronized. | [.NET: CalendarSourceEndpoints.cs:32][N-CALSRC]<br>[.NET: CalendarModule.cs:56][N-CALMOD]<br>[Go: runner.go:42][G-RUNNER] |
| CAL-05 | Remind event participants | Missing | EventReminders recurring job delivers reminders using user preferences. | No reminders or notification delivery. | Upcoming-event alerts are absent. | [.NET: CalendarModule.cs:56][N-CALMOD]<br>[.NET: EventReminders.cs:12][N-CALREM]<br>[Go: api.go:43][G-ROUTES] |

## Notes

Note templates, hashtag extraction, wiki links/backlinks and rename rewriting missing.

| ID | User story | Status | PaperDotNet code/API | PaperGo code/API | Effect / acceptance gap | Code evidence |
| --- | --- | --- | --- | --- | --- | --- |
| NTE-01 | Store Markdown notes and extract inline tags | Missing | Notes module registers a note content type/list template; NoteTagsMutator parses inline Markdown hashtags into keyword fields. | The note field type stores a string; no dedicated notes application behavior. | No built-in note template or automatic hashtag-to-keyword behavior. | [.NET: NotesModule.cs:26][N-NOTEMOD]<br>[.NET: NoteTemplates.cs:7][N-NOTETEMPL]<br>[.NET: NoteMarkdown.cs:14][N-NOTEMD]<br>[Go: fieldtypes.go:24][G-FIELDS] |
| NTE-02 | Follow wiki links/backlinks and preserve links on rename | Missing | GET links/backlinks; NoteLinkIndexer parses wiki links, resolves targets and rewrites references on rename. | Generic relationships require explicit API calls; no wiki parsing, index or rename rewrite. | Linked knowledge-base navigation does not port. | [.NET: NoteEndpoints.cs:29][N-NOTE]<br>[.NET: NoteLinkIndexer.cs:20][N-NOTELINK]<br>[Go: relationships.go:16][G-REL] |

## Provisioning

Schema/taxonomy/smart-folder packages cover a subset; whole-app provisioning and content portability missing.

| ID | User story | Status | PaperDotNet code/API | PaperGo code/API | Effect / acceptance gap | Code evidence |
| --- | --- | --- | --- | --- | --- | --- |
| PRV-01 | Apply/export complete reusable application configuration | Partial | Provisioning template APIs validate XML, parameterize, dry-run and apply module-contributed configuration with keys; export templates. | Workspace schema templates and taxonomy/smart-folder JSON packages only. | Current packages do not recreate workspaces/lists/identity/workflows/module settings as a complete app. | [.NET: ProvisioningEndpoints.cs:29][N-PROV]<br>[.NET: TemplateEngine.cs:18][N-PROVENGINE]<br>[Go: templates.go:14][G-TEMPL]<br>[Go: termexport.go:58][G-TAXPORT]<br>[Go: smartfoldertemplates.go:42][G-SMPORT] |
| PRV-02 | Export/import content packages and monitor the operation | Missing | Async portability exports/imports, downloadable ZIP content packages, operation status/expiry. | No content package export/import or portability operation. | Cannot transfer a complete set of items, file content and module state. | [.NET: Portability.cs:168][N-PORT]<br>[Go: api.go:43][G-ROUTES] |

## ExtensionHost

Per-organization activation/configuration and contributed extension SDK/runtime missing.

| ID | User story | Status | PaperDotNet code/API | PaperGo code/API | Effect / acceptance gap | Code evidence |
| --- | --- | --- | --- | --- | --- | --- |
| EXT-01 | Enable and configure extensions per organization | Missing | Extension listing/enable/disable/settings APIs and tenant runtime state for extensions compiled into the host. | Go has internal extension helpers; no public extension registry or per-tenant runtime. | Administrators cannot activate/configure packaged extensions. | [.NET: ExtensionEndpoints.cs:35][N-EXT]<br>[.NET: ExtensionHostModule.cs:32][N-EXTMOD]<br>[Go: extension.go:14][G-EXT]<br>[Go: api.go:43][G-ROUTES] |
| EXT-02 | Extend field types, APIs, workflow activities and background jobs | Missing | SDK contracts and service registration allow extensions to contribute types, scopes, routes, jobs, triggers and MCP tools. | Static Go registries and internal helpers require building into the app. | Equivalent built-in extensibility for third-party module authors is absent. | [.NET: ExtensionHostModule.cs:32][N-EXTMOD]<br>[.NET: ExtensionRuntime.cs:22][N-SDK]<br>[Go: extension.go:14][G-EXT]<br>[Go: activities.go:146][G-WFACTION]<br>[.NET: Extension.cs:39][N-EXTSDK] |

## Mcp

Authenticated MCP transport and tool catalog missing.

| ID | User story | Status | PaperDotNet code/API | PaperGo code/API | Effect / acceptance gap | Code evidence |
| --- | --- | --- | --- | --- | --- | --- |
| MCP-01 | Use authenticated MCP tools to access items/files/workspaces | Missing | MapMcp streamable HTTP server; built-in item/global-item/folder tools and contributed module tools. | No MCP server or tools in host/router. | Assistant/tool clients cannot use the same integration story. | [.NET: McpModule.cs:33][N-MCP]<br>[.NET: GlobalItemTools.cs:7][N-MCPTOOLS]<br>[Go: main.go:24][G-MAIN]<br>[Go: api.go:43][G-ROUTES] |

## Audit

Workspace audit exists; organization-wide aggregation and investigation filters incomplete.

| ID | User story | Status | PaperDotNet code/API | PaperGo code/API | Effect / acceptance gap | Code evidence |
| --- | --- | --- | --- | --- | --- | --- |
| AUD-01 | Review and filter organization-wide audit history | Partial | GET auditLog aggregates module audit stores; entity type/id, user and date filters; newest first, properties/trace ID. | Workspace-manage audit endpoint with ascending ID cursor, action/subject/details; no entity/user/date filters or cross-module aggregation. | Basic auditing exists, but the same investigation queries and result fields are unavailable. | [.NET: AuditModule.cs:48][N-AUDIT]<br>[Go: audit.go:10][G-AUDIT]<br>[Go: foundation.go:69][G-FOUND] |

## API and operations

HTTP batching and administrative backup/restore absent; error/pagination contracts drift.

| ID | User story | Status | PaperDotNet code/API | PaperGo code/API | Effect / acceptance gap | Code evidence |
| --- | --- | --- | --- | --- | --- | --- |
| API-01 | Send several independent API requests in one HTTP batch | Missing | POST /v1.0/$batch executes subrequests through HTTP pipeline with per-request responses. | Resource bulk operations are atomic DMS mutations, not arbitrary HTTP subrequests. | Clients cannot batch mixed read/write API calls with per-call results. | [.NET: Batch.cs:67][N-BATCH]<br>[Go: bulk.go:10][G-BULK]<br>[Go: api.go:43][G-ROUTES] |
| API-02 | Back up and restore the app through administrative CLI | Missing | Admin CLI backup/restore commands include tenant databases and local file storage. | No corresponding administrative CLI in inspected cmd/api entry point. | No application-provided backup/restore workflow. | [.NET: AdminCli.cs:16][N-CLI]<br>[.NET: BackupService.cs:23][N-BACKUP]<br>[Go: main.go:24][G-MAIN] |
| API-03 | Handle concurrency errors and pagination consistently | Drift | ETag/If-Match; stale versions map to 412; problem responses and OData-style pages/next links. | Version/If-Match support with Go error codes and cursor JSON contract; stale version conflicts return 409. | Same underlying concurrency story exists, but API clients and error handling must be rewritten; no wire compatibility implied. | [.NET: ETags.cs:8][N-ETAG]<br>[.NET: Paging.cs:54][N-PAGE]<br>[Go: api.go:43][G-ROUTES]<br>[Go: api.go:205][G-HTTPERR]<br>[.NET: ApiErrors.cs:26][N-APIERR] |

## Verification and limits

The following existing PaperGo suites passed with a fresh run (`-count=1`):

```text
go test -count=1 ./internal/dms ./internal/httpapi ./internal/workflow ./internal/runner ./internal/webdav
ok  papergo/internal/dms
ok  papergo/internal/httpapi
ok  papergo/internal/workflow
ok  papergo/internal/runner
ok  papergo/internal/webdav
```

These tests establish that the inspected Go implementation passes its own current checks. They do not establish equivalence with PaperDotNet. No application code was changed. Source-line references below were verified against the inspected files. The .NET links are pinned to the audited commit; Go links refer to the local audited files.

The code inventory found 108 explicit Go HTTP method registrations across the router files (including health routes). Its WebDAV handler dispatches additional methods separately. The .NET inventory found 267 endpoint-map call sites across modules, building blocks and host code. Those call-site counts are navigation evidence, not runtime endpoint totals: route groups, conditional registration, multiple methods and extension contributions affect expansion. No endpoint-count ratio is used to claim parity.

UI completeness, performance/load behavior, provider availability, real OAuth/client interoperability and external OCR/AI execution were outside this source-only comparison. Features mentioned only in project prose were not counted. The next step for assurance is to turn the selected acceptance scenarios into a shared harness that checks both apps with equivalent principals, data and inputs.

## Source index

Every detailed finding links to its implementation evidence. For missing Go stories, router/schema/registry links indicate the complete implementation surfaces that were inspected.

| Reference | App | Source file / line |
| --- | --- | --- |
| G-ACL | Go | [internal/dms/permissions.go:30][G-ACL] |
| G-ATTRPATCH | Go | [internal/dms/relationshiptypes.go:185][G-ATTRPATCH] |
| G-AUDIT | Go | [internal/dms/audit.go:10][G-AUDIT] |
| G-AUTH | Go | [internal/auth/auth.go:14][G-AUTH] |
| G-BROWSE | Go | [internal/dms/query.go:43][G-BROWSE] |
| G-BULK | Go | [internal/dms/bulk.go:10][G-BULK] |
| G-CONFIG | Go | [internal/config/config.go:11][G-CONFIG] |
| G-CRED | Go | [internal/dms/credentials.go:36][G-CRED] |
| G-CT | Go | [internal/dms/contenttypes.go:15][G-CT] |
| G-DAV | Go | [internal/webdav/webdav.go:28][G-DAV] |
| G-DAVMETHOD | Go | [internal/webdav/methods.go:45][G-DAVMETHOD] |
| G-DELETE | Go | [internal/dms/service.go:509][G-DELETE] |
| G-EVENT | Go | [internal/dms/events.go:14][G-EVENT] |
| G-EXT | Go | [internal/dms/extension.go:14][G-EXT] |
| G-FIELDS | Go | [internal/dms/fieldtypes.go:24][G-FIELDS] |
| G-FILE | Go | [internal/httpapi/api.go:472][G-FILE] |
| G-FILETEST | Go | [internal/dms/files_test.go:154][G-FILETEST] |
| G-FOUND | Go | [internal/httpapi/foundation.go:69][G-FOUND] |
| G-HTTPERR | Go | [internal/httpapi/api.go:205][G-HTTPERR] |
| G-IMPORT | Go | [internal/dms/termimport.go:40][G-IMPORT] |
| G-KEYWORDS | Go | [internal/dms/keywords.go:20][G-KEYWORDS] |
| G-LOOKUP | Go | [internal/dms/fieldtypes.go:365][G-LOOKUP] |
| G-MAIN | Go | [cmd/api/main.go:24][G-MAIN] |
| G-MOVE | Go | [internal/dms/service.go:446][G-MOVE] |
| G-OPS | Go | [internal/dms/operations.go:23][G-OPS] |
| G-PATCH | Go | [internal/dms/service.go:307][G-PATCH] |
| G-PUB | Go | [internal/dms/content.go:15][G-PUB] |
| G-QUERY | Go | [internal/dms/queryspec.go:30][G-QUERY] |
| G-REL | Go | [internal/dms/relationships.go:16][G-REL] |
| G-RELT | Go | [internal/dms/relationshiptypes.go:12][G-RELT] |
| G-RESOURCE | Go | [ent/schema/resource.go:16][G-RESOURCE] |
| G-ROUTES | Go | [internal/httpapi/api.go:43][G-ROUTES] |
| G-RUNNER | Go | [internal/runner/runner.go:42][G-RUNNER] |
| G-SCHEMA | Go | [internal/dms/revisions.go:119][G-SCHEMA] |
| G-SERVICE | Go | [internal/dms/service.go:129][G-SERVICE] |
| G-SFAPI | Go | [internal/httpapi/smartfolders.go:8][G-SFAPI] |
| G-SMACTION | Go | [internal/dms/smartfolderactions.go:28][G-SMACTION] |
| G-SMART | Go | [internal/dms/smartfolders.go:24][G-SMART] |
| G-SMPORT | Go | [internal/dms/smartfoldertemplates.go:42][G-SMPORT] |
| G-STORE | Go | [internal/storage/local.go:54][G-STORE] |
| G-TAX | Go | [internal/dms/taxonomy.go:62][G-TAX] |
| G-TAXAPI | Go | [internal/httpapi/taxonomy.go:13][G-TAXAPI] |
| G-TAXPORT | Go | [internal/dms/termexport.go:58][G-TAXPORT] |
| G-TEMPL | Go | [internal/dms/templates.go:14][G-TEMPL] |
| G-TERMAUTH | Go | [internal/dms/taxonomy.go:466][G-TERMAUTH] |
| G-TERMVALUE | Go | [internal/dms/termvalues.go:32][G-TERMVALUE] |
| G-TX | Go | [internal/dms/service.go:55][G-TX] |
| G-TYPETEST | Go | [internal/dms/contenttypes_test.go:38][G-TYPETEST] |
| G-VIEW | Go | [ent/schema/listview.go:15][G-VIEW] |
| G-WFACTION | Go | [internal/workflow/activities.go:146][G-WFACTION] |
| G-WFAPI | Go | [internal/httpapi/workflows.go:12][G-WFAPI] |
| G-WFBUILT | Go | [internal/workflow/builtins.go:47][G-WFBUILT] |
| G-WFFORM | Go | [internal/workflow/forms.go:79][G-WFFORM] |
| G-WFRUN | Go | [internal/workflow/run.go:52][G-WFRUN] |
| G-WFSPEC | Go | [internal/workflow/spec.go:44][G-WFSPEC] |
| N-ACCOUNT | .NET | [src/Modules/Identity/PaperDotNet.Identity/Features/AccountEndpoints.cs:58][N-ACCOUNT] |
| N-ACL | .NET | [src/Modules/Lists/PaperDotNet.Lists/Features/PermissionEndpoints.cs:47][N-ACL] |
| N-ACTIONS | .NET | [src/Modules/Workflows/PaperDotNet.Workflows/Features/Actions.cs:105][N-ACTIONS] |
| N-ACTIVITY | .NET | [src/Modules/Collaboration/PaperDotNet.Collaboration/Features/Activity.cs:11][N-ACTIVITY] |
| N-AI | .NET | [src/Modules/AiWorkflows/PaperDotNet.AiWorkflows/AiWorkflowsModule.cs:28][N-AI] |
| N-AIBATCH | .NET | [src/Modules/AiWorkflows/PaperDotNet.AiWorkflows/Features/AiBatch.cs:11][N-AIBATCH] |
| N-AIGATE | .NET | [src/Modules/AiWorkflows/PaperDotNet.AiWorkflows/Features/AiActivities.cs:97][N-AIGATE] |
| N-ALERTS | .NET | [src/Modules/Notifications/PaperDotNet.Notifications/Features/Alerts.cs:19][N-ALERTS] |
| N-APIERR | .NET | [src/BuildingBlocks/PaperDotNet.Api/ApiErrors.cs:26][N-APIERR] |
| N-APP | .NET | [src/Modules/Identity/PaperDotNet.Identity/Features/ApplicationEndpoints.cs:61][N-APP] |
| N-ATTR | .NET | [src/Modules/Lists/PaperDotNet.Lists/Features/RelationshipAttributes.cs:14][N-ATTR] |
| N-AUDIT | .NET | [src/Modules/Audit/PaperDotNet.Audit/AuditModule.cs:48][N-AUDIT] |
| N-AUTH | .NET | [src/Modules/Identity/PaperDotNet.Identity/Features/AuthEndpoints.cs:45][N-AUTH] |
| N-BACKUP | .NET | [src/PaperDotNet.Host/Backup/BackupService.cs:23][N-BACKUP] |
| N-BATCH | .NET | [src/PaperDotNet.Host/Batch.cs:67][N-BATCH] |
| N-BOOK | .NET | [src/Modules/Workflows/PaperDotNet.Workflows/Features/Bookmarks.cs:17][N-BOOK] |
| N-BULK | .NET | [src/Modules/Lists/PaperDotNet.Lists/Features/BulkUpdate.cs:17][N-BULK] |
| N-CAL | .NET | [src/Modules/Calendar/PaperDotNet.Calendar/Features/CalendarEndpoints.cs:50][N-CAL] |
| N-CALMOD | .NET | [src/Modules/Calendar/PaperDotNet.Calendar/CalendarModule.cs:56][N-CALMOD] |
| N-CALREM | .NET | [src/Modules/Calendar/PaperDotNet.Calendar/Features/EventReminders.cs:12][N-CALREM] |
| N-CALSRC | .NET | [src/Modules/Calendar/PaperDotNet.Calendar/Features/CalendarSourceEndpoints.cs:32][N-CALSRC] |
| N-CALTEMPL | .NET | [src/Modules/Calendar/PaperDotNet.Calendar/Features/CalendarTemplates.cs:7][N-CALTEMPL] |
| N-CANDIDATE | .NET | [src/Modules/Documents/PaperDotNet.Documents/Features/DocumentFileStore.cs:57][N-CANDIDATE] |
| N-CHANGE | .NET | [src/Modules/Notifications/PaperDotNet.Notifications/Features/ChangeNotifications.cs:67][N-CHANGE] |
| N-CLI | .NET | [src/PaperDotNet.Host/Cli/AdminCli.cs:16][N-CLI] |
| N-COMMENT | .NET | [src/Modules/Collaboration/PaperDotNet.Collaboration/Features/CommentEndpoints.cs:51][N-COMMENT] |
| N-CT | .NET | [src/Modules/Lists/PaperDotNet.Lists/Features/ContentTypeEndpoints.cs:83][N-CT] |
| N-DELIVERY | .NET | [src/Modules/Notifications/PaperDotNet.Notifications/Features/Delivery.cs:18][N-DELIVERY] |
| N-DELTA | .NET | [src/Modules/Lists/PaperDotNet.Lists/Features/DeltaEndpoints.cs:68][N-DELTA] |
| N-DIR | .NET | [src/Modules/Identity/PaperDotNet.Identity/Features/DirectoryEndpoints.cs:62][N-DIR] |
| N-DOC | .NET | [src/Modules/Documents/PaperDotNet.Documents/Features/DocumentEndpoints.cs:67][N-DOC] |
| N-DOCWF | .NET | [src/Modules/Documents/PaperDotNet.Documents/Features/DocumentWorkflows.cs:50][N-DOCWF] |
| N-ETAG | .NET | [src/BuildingBlocks/PaperDotNet.Api/ETags.cs:8][N-ETAG] |
| N-EXT | .NET | [src/Modules/ExtensionHost/PaperDotNet.ExtensionHost/Features/ExtensionEndpoints.cs:35][N-EXT] |
| N-EXTMOD | .NET | [src/Modules/ExtensionHost/PaperDotNet.ExtensionHost/ExtensionHostModule.cs:32][N-EXTMOD] |
| N-EXTSDK | .NET | [src/Extensions/PaperDotNet.Extensions.Abstractions/Extension.cs:39][N-EXTSDK] |
| N-FIELDBASE | .NET | [src/Modules/Lists/PaperDotNet.Lists.Contracts/Fields.cs:137][N-FIELDBASE] |
| N-FIELDS | .NET | [src/Modules/Lists/PaperDotNet.Lists/Fields/FieldTypes.cs:12][N-FIELDS] |
| N-GC | .NET | [src/Modules/Documents/PaperDotNet.Documents/Features/StorageCleanup.cs:11][N-GC] |
| N-GLOBAL | .NET | [src/Modules/Lists/PaperDotNet.Lists/Features/GlobalItemEndpoints.cs:16][N-GLOBAL] |
| N-HISTORY | .NET | [src/Modules/Lists/PaperDotNet.Lists/Features/ItemHistoryEndpoints.cs:28][N-HISTORY] |
| N-HOME | .NET | [src/Modules/Lists/PaperDotNet.Lists/Features/HomeEndpoints.cs:27][N-HOME] |
| N-HOST | .NET | [src/PaperDotNet.Host/PaperDotNetHost.cs:54][N-HOST] |
| N-ICAL | .NET | [src/Modules/Calendar/PaperDotNet.Calendar/Features/ICalendar.cs:20][N-ICAL] |
| N-INBOX | .NET | [src/Modules/Documents/PaperDotNet.Documents/Features/GroupInboxEndpoints.cs:36][N-INBOX] |
| N-INTAKE | .NET | [src/Modules/Documents/PaperDotNet.Documents/Features/FileIntake.cs:9][N-INTAKE] |
| N-ITEM | .NET | [src/Modules/Lists/PaperDotNet.Lists/Features/ItemEndpoints.cs:18][N-ITEM] |
| N-JOBS | .NET | [src/Modules/Jobs/PaperDotNet.Jobs/JobsModule.cs:12][N-JOBS] |
| N-LIST | .NET | [src/Modules/Lists/PaperDotNet.Lists/Features/ListEndpoints.cs:41][N-LIST] |
| N-LISTMOD | .NET | [src/Modules/Lists/PaperDotNet.Lists/ListsModule.cs:91][N-LISTMOD] |
| N-LOOKUPVALID | .NET | [src/Modules/Lists/PaperDotNet.Lists/Features/ItemWriter.cs:461][N-LOOKUPVALID] |
| N-MCP | .NET | [src/Modules/Mcp/PaperDotNet.Mcp/McpModule.cs:33][N-MCP] |
| N-MCPTOOLS | .NET | [src/Modules/Mcp/PaperDotNet.Mcp/Features/GlobalItemTools.cs:7][N-MCPTOOLS] |
| N-NOTE | .NET | [src/Modules/Notes/PaperDotNet.Notes/Features/NoteEndpoints.cs:29][N-NOTE] |
| N-NOTELINK | .NET | [src/Modules/Notes/PaperDotNet.Notes/Features/NoteLinkIndexer.cs:20][N-NOTELINK] |
| N-NOTEMD | .NET | [src/Modules/Notes/PaperDotNet.Notes/Features/NoteMarkdown.cs:14][N-NOTEMD] |
| N-NOTEMOD | .NET | [src/Modules/Notes/PaperDotNet.Notes/NotesModule.cs:26][N-NOTEMOD] |
| N-NOTETEMPL | .NET | [src/Modules/Notes/PaperDotNet.Notes/Features/NoteTemplates.cs:7][N-NOTETEMPL] |
| N-NOTIFY | .NET | [src/Modules/Notifications/PaperDotNet.Notifications/Features/NotificationEndpoints.cs:55][N-NOTIFY] |
| N-NOTIFYACT | .NET | [src/Modules/Notifications/PaperDotNet.Notifications/Features/NotifyActivity.cs:11][N-NOTIFYACT] |
| N-NTFMOD | .NET | [src/Modules/Notifications/PaperDotNet.Notifications/NotificationsModule.cs:49][N-NTFMOD] |
| N-OAUTH | .NET | [src/Modules/Identity/PaperDotNet.Identity/Features/OAuthEndpoints.cs:34][N-OAUTH] |
| N-OPS | .NET | [src/Modules/Jobs/PaperDotNet.Jobs/Features/Operations.cs:174][N-OPS] |
| N-ORG | .NET | [src/Modules/Tenancy/PaperDotNet.Tenancy/Features/OrganizationEndpoints.cs:20][N-ORG] |
| N-PAGE | .NET | [src/BuildingBlocks/PaperDotNet.Api/Paging.cs:54][N-PAGE] |
| N-PAGES | .NET | [src/Modules/Documents/PaperDotNet.Documents/Features/PageOperations.cs:49][N-PAGES] |
| N-PORT | .NET | [src/Modules/Provisioning/PaperDotNet.Provisioning/Features/Portability.cs:168][N-PORT] |
| N-PREF | .NET | [src/Modules/Identity/PaperDotNet.Identity/Features/PreferencesEndpoints.cs:80][N-PREF] |
| N-PROV | .NET | [src/Modules/Provisioning/PaperDotNet.Provisioning/Features/ProvisioningEndpoints.cs:29][N-PROV] |
| N-PROVENGINE | .NET | [src/Modules/Provisioning/PaperDotNet.Provisioning/Features/TemplateEngine.cs:18][N-PROVENGINE] |
| N-QUERY | .NET | [src/Modules/Lists/PaperDotNet.Lists/Querying/ItemQueryRunner.cs:59][N-QUERY] |
| N-REL | .NET | [src/Modules/Lists/PaperDotNet.Lists/Features/ItemRelationshipEndpoints.cs:15][N-REL] |
| N-RELRULES | .NET | [src/Modules/Lists/PaperDotNet.Lists/Features/ListItemStore.cs:204][N-RELRULES] |
| N-RUN | .NET | [src/Modules/Workflows/PaperDotNet.Workflows/Features/Runs.cs:583][N-RUN] |
| N-SCRIPT | .NET | [src/Modules/Workflows/PaperDotNet.Workflows/Features/Scripts.cs:47][N-SCRIPT] |
| N-SDK | .NET | [src/Modules/ExtensionHost/PaperDotNet.ExtensionHost/Runtime/ExtensionRuntime.cs:22][N-SDK] |
| N-SEARCH | .NET | [src/Modules/Search/PaperDotNet.Search/Features/SearchEndpoints.cs:26][N-SEARCH] |
| N-SEM | .NET | [src/Modules/Search/PaperDotNet.Search/Features/Semantic.cs:15][N-SEM] |
| N-SMART | .NET | [src/Modules/Lists/PaperDotNet.Lists/Features/SmartFolders.cs:83][N-SMART] |
| N-SMARTT | .NET | [src/Modules/Lists/PaperDotNet.Lists/Features/SmartFolderTemplates.cs:17][N-SMARTT] |
| N-SPEC | .NET | [src/Modules/Workflows/PaperDotNet.Workflows/Features/Definitions.cs:20][N-SPEC] |
| N-TASK | .NET | [src/Modules/Tasks/PaperDotNet.Tasks/Features/TaskEndpoints.cs:55][N-TASK] |
| N-TASKMOD | .NET | [src/Modules/Tasks/PaperDotNet.Tasks/TasksModule.cs:38][N-TASKMOD] |
| N-TASKREC | .NET | [src/Modules/Tasks/PaperDotNet.Tasks/Features/Recurrences.cs:14][N-TASKREC] |
| N-TASKTEMPL | .NET | [src/Modules/Tasks/PaperDotNet.Tasks/Features/TaskTemplates.cs:6][N-TASKTEMPL] |
| N-TAX | .NET | [src/Modules/Taxonomy/PaperDotNet.Taxonomy/Features/TermStoreEndpoints.cs:104][N-TAX] |
| N-TAXTERMS | .NET | [src/Modules/Taxonomy/PaperDotNet.Taxonomy/Features/TermStoreEndpoints.Terms.cs:99][N-TAXTERMS] |
| N-TEMPL | .NET | [src/Modules/Lists/PaperDotNet.Lists/Templates/BuiltInTemplates.cs:21][N-TEMPL] |
| N-TEN | .NET | [src/Modules/Tenancy/PaperDotNet.Tenancy/Resolution/HostMappingStrategy.cs:7][N-TEN] |
| N-TENDIR | .NET | [src/Modules/Tenancy/PaperDotNet.Tenancy/Features/TenantDirectory.cs:12][N-TENDIR] |
| N-TOKEN | .NET | [src/Modules/Identity/PaperDotNet.Identity/Features/MeEndpoints.cs:50][N-TOKEN] |
| N-VIEW | .NET | [src/Modules/Lists/PaperDotNet.Lists/Features/ViewEndpoints.cs:18][N-VIEW] |
| N-WF | .NET | [src/Modules/Workflows/PaperDotNet.Workflows/Features/WorkflowEndpoints.cs:111][N-WF] |
| N-WFMOD | .NET | [src/Modules/Workflows/PaperDotNet.Workflows/WorkflowsModule.cs:73][N-WFMOD] |
| N-WRITER | .NET | [src/Modules/Lists/PaperDotNet.Lists/Features/ItemWriter.cs:31][N-WRITER] |
| N-WS | .NET | [src/Modules/Workspaces/PaperDotNet.Workspaces/Features/WorkspaceEndpoints.cs:44][N-WS] |

[G-ACL]: C:/Users/eduapaul/Documents/PaperGo/internal/dms/permissions.go:30
[G-ATTRPATCH]: C:/Users/eduapaul/Documents/PaperGo/internal/dms/relationshiptypes.go:185
[G-AUDIT]: C:/Users/eduapaul/Documents/PaperGo/internal/dms/audit.go:10
[G-AUTH]: C:/Users/eduapaul/Documents/PaperGo/internal/auth/auth.go:14
[G-BROWSE]: C:/Users/eduapaul/Documents/PaperGo/internal/dms/query.go:43
[G-BULK]: C:/Users/eduapaul/Documents/PaperGo/internal/dms/bulk.go:10
[G-CONFIG]: C:/Users/eduapaul/Documents/PaperGo/internal/config/config.go:11
[G-CRED]: C:/Users/eduapaul/Documents/PaperGo/internal/dms/credentials.go:36
[G-CT]: C:/Users/eduapaul/Documents/PaperGo/internal/dms/contenttypes.go:15
[G-DAV]: C:/Users/eduapaul/Documents/PaperGo/internal/webdav/webdav.go:28
[G-DAVMETHOD]: C:/Users/eduapaul/Documents/PaperGo/internal/webdav/methods.go:45
[G-DELETE]: C:/Users/eduapaul/Documents/PaperGo/internal/dms/service.go:509
[G-EVENT]: C:/Users/eduapaul/Documents/PaperGo/internal/dms/events.go:14
[G-EXT]: C:/Users/eduapaul/Documents/PaperGo/internal/dms/extension.go:14
[G-FIELDS]: C:/Users/eduapaul/Documents/PaperGo/internal/dms/fieldtypes.go:24
[G-FILE]: C:/Users/eduapaul/Documents/PaperGo/internal/httpapi/api.go:472
[G-FILETEST]: C:/Users/eduapaul/Documents/PaperGo/internal/dms/files_test.go:154
[G-FOUND]: C:/Users/eduapaul/Documents/PaperGo/internal/httpapi/foundation.go:69
[G-HTTPERR]: C:/Users/eduapaul/Documents/PaperGo/internal/httpapi/api.go:205
[G-IMPORT]: C:/Users/eduapaul/Documents/PaperGo/internal/dms/termimport.go:40
[G-KEYWORDS]: C:/Users/eduapaul/Documents/PaperGo/internal/dms/keywords.go:20
[G-LOOKUP]: C:/Users/eduapaul/Documents/PaperGo/internal/dms/fieldtypes.go:365
[G-MAIN]: C:/Users/eduapaul/Documents/PaperGo/cmd/api/main.go:24
[G-MOVE]: C:/Users/eduapaul/Documents/PaperGo/internal/dms/service.go:446
[G-OPS]: C:/Users/eduapaul/Documents/PaperGo/internal/dms/operations.go:23
[G-PATCH]: C:/Users/eduapaul/Documents/PaperGo/internal/dms/service.go:307
[G-PUB]: C:/Users/eduapaul/Documents/PaperGo/internal/dms/content.go:15
[G-QUERY]: C:/Users/eduapaul/Documents/PaperGo/internal/dms/queryspec.go:30
[G-REL]: C:/Users/eduapaul/Documents/PaperGo/internal/dms/relationships.go:16
[G-RELT]: C:/Users/eduapaul/Documents/PaperGo/internal/dms/relationshiptypes.go:12
[G-RESOURCE]: C:/Users/eduapaul/Documents/PaperGo/ent/schema/resource.go:16
[G-ROUTES]: C:/Users/eduapaul/Documents/PaperGo/internal/httpapi/api.go:43
[G-RUNNER]: C:/Users/eduapaul/Documents/PaperGo/internal/runner/runner.go:42
[G-SCHEMA]: C:/Users/eduapaul/Documents/PaperGo/internal/dms/revisions.go:119
[G-SERVICE]: C:/Users/eduapaul/Documents/PaperGo/internal/dms/service.go:129
[G-SFAPI]: C:/Users/eduapaul/Documents/PaperGo/internal/httpapi/smartfolders.go:8
[G-SMACTION]: C:/Users/eduapaul/Documents/PaperGo/internal/dms/smartfolderactions.go:28
[G-SMART]: C:/Users/eduapaul/Documents/PaperGo/internal/dms/smartfolders.go:24
[G-SMPORT]: C:/Users/eduapaul/Documents/PaperGo/internal/dms/smartfoldertemplates.go:42
[G-STORE]: C:/Users/eduapaul/Documents/PaperGo/internal/storage/local.go:54
[G-TAX]: C:/Users/eduapaul/Documents/PaperGo/internal/dms/taxonomy.go:50
[G-TAXAPI]: C:/Users/eduapaul/Documents/PaperGo/internal/httpapi/taxonomy.go:13
[G-TAXPORT]: C:/Users/eduapaul/Documents/PaperGo/internal/dms/termexport.go:58
[G-TEMPL]: C:/Users/eduapaul/Documents/PaperGo/internal/dms/templates.go:14
[G-TERMAUTH]: C:/Users/eduapaul/Documents/PaperGo/internal/dms/taxonomy.go:437
[G-TERMVALUE]: C:/Users/eduapaul/Documents/PaperGo/internal/dms/termvalues.go:32
[G-TX]: C:/Users/eduapaul/Documents/PaperGo/internal/dms/service.go:55
[G-TYPETEST]: C:/Users/eduapaul/Documents/PaperGo/internal/dms/contenttypes_test.go:38
[G-VIEW]: C:/Users/eduapaul/Documents/PaperGo/ent/schema/listview.go:15
[G-WFACTION]: C:/Users/eduapaul/Documents/PaperGo/internal/workflow/activities.go:146
[G-WFAPI]: C:/Users/eduapaul/Documents/PaperGo/internal/httpapi/workflows.go:12
[G-WFBUILT]: C:/Users/eduapaul/Documents/PaperGo/internal/workflow/builtins.go:47
[G-WFFORM]: C:/Users/eduapaul/Documents/PaperGo/internal/workflow/forms.go:79
[G-WFRUN]: C:/Users/eduapaul/Documents/PaperGo/internal/workflow/run.go:52
[G-WFSPEC]: C:/Users/eduapaul/Documents/PaperGo/internal/workflow/spec.go:44
[N-ACCOUNT]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Identity/PaperDotNet.Identity/Features/AccountEndpoints.cs#L58
[N-ACL]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Lists/PaperDotNet.Lists/Features/PermissionEndpoints.cs#L47
[N-ACTIONS]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Workflows/PaperDotNet.Workflows/Features/Actions.cs#L105
[N-ACTIVITY]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Collaboration/PaperDotNet.Collaboration/Features/Activity.cs#L11
[N-AI]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/AiWorkflows/PaperDotNet.AiWorkflows/AiWorkflowsModule.cs#L28
[N-AIBATCH]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/AiWorkflows/PaperDotNet.AiWorkflows/Features/AiBatch.cs#L11
[N-AIGATE]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/AiWorkflows/PaperDotNet.AiWorkflows/Features/AiActivities.cs#L97
[N-ALERTS]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Notifications/PaperDotNet.Notifications/Features/Alerts.cs#L19
[N-APIERR]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/BuildingBlocks/PaperDotNet.Api/ApiErrors.cs#L26
[N-APP]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Identity/PaperDotNet.Identity/Features/ApplicationEndpoints.cs#L61
[N-ATTR]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Lists/PaperDotNet.Lists/Features/RelationshipAttributes.cs#L14
[N-AUDIT]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Audit/PaperDotNet.Audit/AuditModule.cs#L48
[N-AUTH]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Identity/PaperDotNet.Identity/Features/AuthEndpoints.cs#L45
[N-BACKUP]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/PaperDotNet.Host/Backup/BackupService.cs#L23
[N-BATCH]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/PaperDotNet.Host/Batch.cs#L67
[N-BOOK]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Workflows/PaperDotNet.Workflows/Features/Bookmarks.cs#L17
[N-BULK]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Lists/PaperDotNet.Lists/Features/BulkUpdate.cs#L17
[N-CAL]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Calendar/PaperDotNet.Calendar/Features/CalendarEndpoints.cs#L50
[N-CALMOD]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Calendar/PaperDotNet.Calendar/CalendarModule.cs#L56
[N-CALREM]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Calendar/PaperDotNet.Calendar/Features/EventReminders.cs#L12
[N-CALSRC]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Calendar/PaperDotNet.Calendar/Features/CalendarSourceEndpoints.cs#L32
[N-CALTEMPL]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Calendar/PaperDotNet.Calendar/Features/CalendarTemplates.cs#L7
[N-CANDIDATE]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Documents/PaperDotNet.Documents/Features/DocumentFileStore.cs#L57
[N-CHANGE]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Notifications/PaperDotNet.Notifications/Features/ChangeNotifications.cs#L67
[N-CLI]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/PaperDotNet.Host/Cli/AdminCli.cs#L16
[N-COMMENT]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Collaboration/PaperDotNet.Collaboration/Features/CommentEndpoints.cs#L51
[N-CT]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Lists/PaperDotNet.Lists/Features/ContentTypeEndpoints.cs#L83
[N-DELIVERY]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Notifications/PaperDotNet.Notifications/Features/Delivery.cs#L18
[N-DELTA]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Lists/PaperDotNet.Lists/Features/DeltaEndpoints.cs#L68
[N-DIR]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Identity/PaperDotNet.Identity/Features/DirectoryEndpoints.cs#L62
[N-DOC]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Documents/PaperDotNet.Documents/Features/DocumentEndpoints.cs#L67
[N-DOCWF]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Documents/PaperDotNet.Documents/Features/DocumentWorkflows.cs#L50
[N-ETAG]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/BuildingBlocks/PaperDotNet.Api/ETags.cs#L8
[N-EXT]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/ExtensionHost/PaperDotNet.ExtensionHost/Features/ExtensionEndpoints.cs#L35
[N-EXTMOD]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/ExtensionHost/PaperDotNet.ExtensionHost/ExtensionHostModule.cs#L32
[N-EXTSDK]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Extensions/PaperDotNet.Extensions.Abstractions/Extension.cs#L39
[N-FIELDBASE]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Lists/PaperDotNet.Lists.Contracts/Fields.cs#L137
[N-FIELDS]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Lists/PaperDotNet.Lists/Fields/FieldTypes.cs#L12
[N-GC]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Documents/PaperDotNet.Documents/Features/StorageCleanup.cs#L11
[N-GLOBAL]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Lists/PaperDotNet.Lists/Features/GlobalItemEndpoints.cs#L16
[N-HISTORY]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Lists/PaperDotNet.Lists/Features/ItemHistoryEndpoints.cs#L28
[N-HOME]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Lists/PaperDotNet.Lists/Features/HomeEndpoints.cs#L27
[N-HOST]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/PaperDotNet.Host/PaperDotNetHost.cs#L54
[N-ICAL]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Calendar/PaperDotNet.Calendar/Features/ICalendar.cs#L20
[N-INBOX]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Documents/PaperDotNet.Documents/Features/GroupInboxEndpoints.cs#L36
[N-INTAKE]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Documents/PaperDotNet.Documents/Features/FileIntake.cs#L9
[N-ITEM]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Lists/PaperDotNet.Lists/Features/ItemEndpoints.cs#L18
[N-JOBS]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Jobs/PaperDotNet.Jobs/JobsModule.cs#L12
[N-LIST]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Lists/PaperDotNet.Lists/Features/ListEndpoints.cs#L41
[N-LISTMOD]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Lists/PaperDotNet.Lists/ListsModule.cs#L91
[N-LOOKUPVALID]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Lists/PaperDotNet.Lists/Features/ItemWriter.cs#L461
[N-MCP]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Mcp/PaperDotNet.Mcp/McpModule.cs#L33
[N-MCPTOOLS]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Mcp/PaperDotNet.Mcp/Features/GlobalItemTools.cs#L7
[N-NOTE]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Notes/PaperDotNet.Notes/Features/NoteEndpoints.cs#L29
[N-NOTELINK]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Notes/PaperDotNet.Notes/Features/NoteLinkIndexer.cs#L20
[N-NOTEMD]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Notes/PaperDotNet.Notes/Features/NoteMarkdown.cs#L14
[N-NOTEMOD]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Notes/PaperDotNet.Notes/NotesModule.cs#L26
[N-NOTETEMPL]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Notes/PaperDotNet.Notes/Features/NoteTemplates.cs#L7
[N-NOTIFY]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Notifications/PaperDotNet.Notifications/Features/NotificationEndpoints.cs#L55
[N-NOTIFYACT]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Notifications/PaperDotNet.Notifications/Features/NotifyActivity.cs#L11
[N-NTFMOD]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Notifications/PaperDotNet.Notifications/NotificationsModule.cs#L49
[N-OAUTH]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Identity/PaperDotNet.Identity/Features/OAuthEndpoints.cs#L34
[N-OPS]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Jobs/PaperDotNet.Jobs/Features/Operations.cs#L174
[N-ORG]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Tenancy/PaperDotNet.Tenancy/Features/OrganizationEndpoints.cs#L20
[N-PAGE]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/BuildingBlocks/PaperDotNet.Api/Paging.cs#L54
[N-PAGES]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Documents/PaperDotNet.Documents/Features/PageOperations.cs#L49
[N-PORT]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Provisioning/PaperDotNet.Provisioning/Features/Portability.cs#L168
[N-PREF]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Identity/PaperDotNet.Identity/Features/PreferencesEndpoints.cs#L80
[N-PROV]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Provisioning/PaperDotNet.Provisioning/Features/ProvisioningEndpoints.cs#L29
[N-PROVENGINE]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Provisioning/PaperDotNet.Provisioning/Features/TemplateEngine.cs#L18
[N-QUERY]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Lists/PaperDotNet.Lists/Querying/ItemQueryRunner.cs#L59
[N-REL]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Lists/PaperDotNet.Lists/Features/ItemRelationshipEndpoints.cs#L15
[N-RELRULES]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Lists/PaperDotNet.Lists/Features/ListItemStore.cs#L204
[N-RUN]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Workflows/PaperDotNet.Workflows/Features/Runs.cs#L583
[N-SCRIPT]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Workflows/PaperDotNet.Workflows/Features/Scripts.cs#L47
[N-SDK]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/ExtensionHost/PaperDotNet.ExtensionHost/Runtime/ExtensionRuntime.cs#L22
[N-SEARCH]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Search/PaperDotNet.Search/Features/SearchEndpoints.cs#L26
[N-SEM]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Search/PaperDotNet.Search/Features/Semantic.cs#L15
[N-SMART]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Lists/PaperDotNet.Lists/Features/SmartFolders.cs#L83
[N-SMARTT]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Lists/PaperDotNet.Lists/Features/SmartFolderTemplates.cs#L17
[N-SPEC]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Workflows/PaperDotNet.Workflows/Features/Definitions.cs#L20
[N-TASK]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Tasks/PaperDotNet.Tasks/Features/TaskEndpoints.cs#L55
[N-TASKMOD]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Tasks/PaperDotNet.Tasks/TasksModule.cs#L38
[N-TASKREC]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Tasks/PaperDotNet.Tasks/Features/Recurrences.cs#L14
[N-TASKTEMPL]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Tasks/PaperDotNet.Tasks/Features/TaskTemplates.cs#L6
[N-TAX]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Taxonomy/PaperDotNet.Taxonomy/Features/TermStoreEndpoints.cs#L104
[N-TAXTERMS]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Taxonomy/PaperDotNet.Taxonomy/Features/TermStoreEndpoints.Terms.cs#L99
[N-TEMPL]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Lists/PaperDotNet.Lists/Templates/BuiltInTemplates.cs#L21
[N-TEN]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Tenancy/PaperDotNet.Tenancy/Resolution/HostMappingStrategy.cs#L7
[N-TENDIR]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Tenancy/PaperDotNet.Tenancy/Features/TenantDirectory.cs#L12
[N-TOKEN]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Identity/PaperDotNet.Identity/Features/MeEndpoints.cs#L50
[N-VIEW]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Lists/PaperDotNet.Lists/Features/ViewEndpoints.cs#L18
[N-WF]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Workflows/PaperDotNet.Workflows/Features/WorkflowEndpoints.cs#L111
[N-WFMOD]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Workflows/PaperDotNet.Workflows/WorkflowsModule.cs#L73
[N-WRITER]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Lists/PaperDotNet.Lists/Features/ItemWriter.cs#L31
[N-WS]: https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/src/Modules/Workspaces/PaperDotNet.Workspaces/Features/WorkspaceEndpoints.cs#L44
