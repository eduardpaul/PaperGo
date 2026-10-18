# PaperGo Product & Technical Backlog

This backlog synthesizes user feedback and feature requests into actionable engineering specifications for PaperGo. In accordance with PaperGo's architecture guidelines, implementations adhere strictly to the principle of **clean, current-state code without legacy shims, backwards-compatibility hacks, or dual code paths**.

---

## Table of Contents

1. [Backlog Summary Matrix](#1-backlog-summary-matrix)
2. [UI & UX Enhancements](#2-ui--ux-enhancements)
   - [ISSUE-01: Right-Click Context Menu Actions](#issue-01-right-click-context-menu-actions)
   - [ISSUE-02: Workspace Creation Optional Description](#issue-02-workspace-creation-optional-description)
   - [ISSUE-03: Search Input Focus Loss During Active Typing](#issue-03-search-input-focus-loss-during-active-typing)
   - [ISSUE-04: Windows Drag-and-Drop Upload Failure](#issue-04-windows-drag-and-drop-upload-failure)
   - [ISSUE-05: Drag-and-Drop Item Move into Folders](#issue-05-drag-and-drop-item-move-into-folders)
   - [ISSUE-06: Currency Field Preset Codes and Defaults](#issue-06-currency-field-preset-codes-and-defaults)
   - [ISSUE-07: Smart Folders UX/UI Rework](#issue-07-smart-folders-uxui-rework)
   - [ISSUE-08: Global Search Content Type Filtering & Relationships Section](#issue-08-global-search-content-type-filtering--relationships-section)
3. [DMS, Content & Storage Lifecycle](#3-dms-content--storage-lifecycle)
   - [ISSUE-09: Default Versioning History Disabled on Lists and Libraries](#issue-09-default-versioning-history-disabled-on-lists-and-libraries)
   - [ISSUE-10: File Renaming Flow During and After Upload](#issue-10-file-renaming-during-and-after-upload)
   - [ISSUE-11: System Fields and Tag Visibility in Field Definitions](#issue-11-system-fields-and-tag-visibility-in-field-definitions)
   - [ISSUE-24: Changing Any Field in a List Schema](#issue-24-changing-any-field-in-a-list-schema)
   - [ISSUE-13: Workspace Package Export Publications Reference Fix](#issue-13-workspace-package-export-publications-reference-fix)
   - [ISSUE-23: Simplified SQLite Migrations via sqlite3def (sqldef) and Embedded Sequential Upgrades](#issue-23-simplified-sqlite-migrations-via-sqlite3def-sqldef-and-embedded-sequential-upgrades)
   - [ISSUE-25: Retention for Scheduled Maintenance Executions](#issue-25-retention-for-scheduled-maintenance-executions)
   - [ISSUE-26: Compact Transactional Step-Result Storage](#issue-26-compact-transactional-step-result-storage)
   - [ISSUE-27: Deduplicate Durable Workflow Payloads](#issue-27-deduplicate-durable-workflow-payloads)
   - [ISSUE-28: Operational Retention Policies and Cleanup Cadence](#issue-28-operational-retention-policies-and-cleanup-cadence)
   - [ISSUE-29: WAL Allocation and Checkpoint Controls](#issue-29-wal-allocation-and-checkpoint-controls)
   - [ISSUE-30: Storage Diagnostics and Growth Regression Coverage](#issue-30-storage-diagnostics-and-growth-regression-coverage)
4. [Workflow Engine & Automation](#4-workflow-engine--automation)
   - [ISSUE-14: Granular `file.updated` vs `item.updated` Events](#issue-14-granular-fileupdated-vs-itemupdated-events)
   - [ISSUE-15: Manual Workflow Execution on Selected Item(s)](#issue-15-manual-workflow-execution-on-selected-items)
   - [ISSUE-16: Workflow Authoring Item-Cardinality Trigger Configuration](#issue-16-workflow-authoring-item-cardinality-trigger-configuration)
   - [ISSUE-17: Item Details Workflows Panel & Execution History](#issue-17-item-details-workflows-panel--execution-history)
   - [ISSUE-18: On-Demand (Manual) Built-in Workflow Variants](#issue-18-on-demand-manual-built-in-workflow-variants)
   - [ISSUE-19: Asynchronous Low-Resolution Blob Preview Pipeline](#issue-19-asynchronous-low-resolution-blob-preview-pipeline)
5. [Integrations, Identity & Samples](#5-integrations-identity--samples)
   - [ISSUE-20: Admin-Managed REST Access Tokens for External Integrations](#issue-20-admin-managed-rest-access-tokens-for-external-integrations)
   - [ISSUE-21: NPM Docker Compose iCal Integration DNS Resolution](#issue-21-npm-docker-compose-ical-integration-dns-resolution)
   - [ISSUE-22: NPM Sample Native Vector Search with Zvec](#issue-22-npm-sample-native-vector-search-with-zvec)
6. [Major Epic: Standalone Single Binary Distribution](#6-major-epic-standalone-single-binary-distribution)
   - [FEAT-01: Zero-Dependency Single Binary Executable (Linux & Windows)](#feat-01-zero-dependency-single-binary-executable-linux--windows)
7. [Implementation Roadmap](#7-implementation-roadmap)

---

## Phase 1 Progress

**Completed 2026-10-16.** All four fixes are implemented. Final validation passed:
DMS, workflow runner, HTTP API, database, WebDAV and backup suites; 137 frontend
tests; production build, typecheck, lint, scoped Go vet, Ent generation and Atlas
migration validation. Windows file-drop behavior has automated drag-event coverage;
manual Chrome/Edge validation on Windows remains unperformed.

- [x] **ISSUE-03** — Search input remains mounted across query updates, follows URL navigation, and clears immediately while retaining focus. Validation: collection-page regression tests and frontend typecheck.
- [x] **ISSUE-04** — Normalize file drag types, inspect file items, cancel browser drop navigation, and enqueue all files with copy feedback. Validation: 12 collection-page regressions (including read-only and non-file drags) and frontend typecheck.
- [x] **ISSUE-13** — Export publications independently of collected revisions, preserve snapshots and typed references, and accept optional revision links on import. Validation: workspace package suite, including publishing-off and unpublish round trips, dry runs, source immutability, and strict live-reference rejection; Ent regenerated and Atlas checksum updated.
- [x] **ISSUE-09** — Lists, libraries, Documents templates and Home default to history off; explicit major versioning remains opt-in. Replacement files enter protected byte reclamation with a one-hour grace period; re-uploads exclude reclaimed objects from deduplication. Settings/history copy and API/docs match. Validation: full DMS, HTTP API and database suites, 137 frontend tests, build, typecheck, lint, and scoped Go vet.

## Phase 2 Progress

**Completed 2026-10-16.** All five in-scope fixes are implemented. General list-schema field editing is deferred to ISSUE-24. Final validation passed: the complete Go suite, 146 frontend tests, frontend lint and production build/typecheck, Go vet and application build, Ent regeneration, and Atlas migration validation.

- [x] **ISSUE-14** — First attachments emit `file.added`; replacements, restores and content reindexing emit `file.updated` with filename and previous blob. Built-ins, trigger filters, editor, samples and API/docs agree. Validation: file/content/search/staging/definition and sample-package suites across DMS, workflow, runner and HTTP API; frontend typecheck.
- [x] **ISSUE-15** — Selection toolbar offers enabled manual workflows matching the collection and item count. Launches keep the selected items, render typed input forms, show authorization failures inline and link to run details. Explicit starts keep inspectable run IDs: coalescing preserves each invocation and busy skip policies record a visible rejection. Validation: 16 workflow launch/collection regressions, typecheck and lint.
- [x] **ISSUE-16** — Editor exposes standalone, single-item and multiple-item association, with any-workspace or collection scope and selection-token guidance. Launch forms and server enforce 0/1/N items; multi-collection selections use the primary item’s collection. Validation: manual/selection/definition/API suites and association/launch UI regressions, typecheck and lint.
- [x] **ISSUE-17** — Item Workflows tab provides manual launches, paged live summaries with status/trigger/actor/timing, and manager links to node details and retry. Draft readers receive item-scoped summaries without execution payloads or other member IDs. Validation: summary/selection/API authorization tests, item history and retry UI regressions, typecheck and lint.
- [x] **ISSUE-10** — Rename action and Details edits freeze per-revision attachment display names, preserve extensions and published/history names, and update downloads/WebDAV/workflow reads without file processing. Upload name conflicts offer rename-and-retry or conditional replacement only for writable targets. Validation: rename/upload/file/history/portability/API/WebDAV suites, 20 upload/item UI regressions, typecheck and lint; Ent and the single schema/checksum updated.

## Phase 3 Progress

- [x] **ISSUE-01** — Radix context menus on sidebar workspaces/collections, workspace cards, table rows, gallery cards and board cards. Fresh permissions gate actions; selected rows retain their targets for workflow, move and delete actions. Folder/item selections move atomically through a bounded endpoint with version, ACL, overlap and cycle checks. Validation: context/navigation/selection/move UI regressions, DMS/API move/bulk suites, typecheck and lint.
- [x] **ISSUE-05** — Internal drags move items/folders and explicit selections onto folder rows/cards or breadcrumbs with move feedback. File-upload drags stay separate. Versioned atomic moves reject cycles/overlaps and report failures; name conflicts offer rename, or confirmed atomic replacement for a writable single file. Validation: table/breadcrumb/selection/parser/conflict UI tests, transactional move/replacement and ACL/API regressions, typecheck and lint.
- [x] **ISSUE-06** — Search currency presets by code, symbol or English name, with custom three-letter codes. Organization defaults start at USD; workspace managers can override or inherit, and new field forms preselect the effective default. Validation: currency/preferences/settings API and DMS regressions, 12 picker/form tests, typecheck, Ent generation and Atlas validation; sample packages regenerated.
- [x] **ISSUE-11** — Item and collection inspectors separate system properties from custom fields, including file metadata, authors and assigned tags/keywords. Item metadata and schema use the same authorized visible revision. Validation: frozen schema/publication/file-surface regressions, inspector UI test and frontend typecheck.
- [x] **ISSUE-08** — Search content-type selector, URL filters/chips and facets use a catalog scoped to readable collections. Expandable relationship badges load authorized incoming/outgoing links with direct item navigation and paging. Validation: scoped catalog permission tests, search API suite, six search UI regressions and typecheck.
- [x] **ISSUE-07** — Saved searches and classification folders have explicit modes, labelled typed rules, scope/type/keyword selectors and read-only live previews. Results open directly in table/grid layouts with collection badges, sorting, paging and optional group browsing; sidebar menus edit/delete authorized definitions. Classification uses a destination folder browser. Validation: preview ACL/side-effect/file-surface checks, mode and cursor-order regressions, scoped field metadata and HTTP/OpenAPI tests, frontend regressions, regenerated sample packages.


Phase 3 is complete. Final validation: full Go suite (988 test cases passed, 15 optional integration cases skipped), frontend typecheck/lint and all 167 tests, all 53 Python script/sample tests, CI-pinned staticcheck, `go vet ./...`, module checksum verification, reproducible Ent generation, Atlas migration validation and production frontend/application builds. Sample workspace packages were regenerated and verified.

## Phase 4 Progress

Scope: ISSUE-21, ISSUE-22, ISSUE-20 and ISSUE-19. ISSUE-23 and FEAT-01 are deferred and excluded from this phase.

- [x] **ISSUE-21** — Backend Docker bridge allows outbound DNS/HTTPS while PaperGo ports remain unpublished. Setup and verification check calendar.google.com from the actual container with TLS verification and actionable errors. Validation: 20 NPM Python tests, admin CLI tests, Compose configuration checks and live distroless-container DNS/HTTPS smoke test.
- [x] **ISSUE-22** — NPM selects native zvec with a private persistent search directory owned by UID 65532. Setup requires live zvec status; verification also waits for a newly created item to appear in keyword search. Validation: 21 NPM Python tests, full Docker image build, fresh-volume permissions/schema startup, live zvec status and native keyword-index smoke test.
- [x] **ISSUE-20** — Organization managers list/revoke organization credentials and issue scoped `pgo_` integration tokens with separate non-interactive service identities, 30/90/365-day or no expiration, one-time secrets and last-use metadata. Token scopes and resource ACLs apply in bearer/proxy modes and queued operations revalidate the credential. Validation: issuance/ACL/scope/expiry/disabled-identity/revocation and proxy API tests, personal-token/operation regressions, two admin UI tests, typecheck/lint, production frontend build, Ent/Atlas checks and sample-package regeneration.
- [x] **ISSUE-19** — Default file-preview system workflow renders first-page PDF/TIFF and static-image WebP renditions under 100,000 bytes and 800 pixels asynchronously, with separate bounded preview work. Authorized viewing follows the caller’s visible revision, preserves originals, shares identical bytes and supports pending/unavailable placeholders. Gallery/board/content/selection views use previews. Packages, backup and cleanup preserve their lifecycle. Validation: 21.9 MB source → 77 KB in about 0.6 s, PDF/TIFF bounds, ACL/publication tests, asynchronous reuse/replacement and worker starvation tests, package/backup/cleanup regressions and preview UI tests.


**Completed the four selected Phase 4 issues on 2026-10-17.** Final validation: all 1,000 Go test cases passed (15 optional integration cases skipped), all 171 frontend tests and all 56 Python tests passed, with frontend typecheck/lint, production API/admin/frontend builds, full Go vet/staticcheck, module checksum verification, reproducible Ent generation, Atlas validation and regenerated sample packages. The built Docker image passed fresh-volume startup, native zvec indexing, outbound calendar DNS/HTTPS, scoped service-token authentication/revocation and asynchronous preview/original-preservation checks. ISSUE-23 and FEAT-01 remain deferred.

**CI follow-up 2026-10-17:** The module minimum and documented build requirement are Go 1.27, matching the workflow JSON API and the Go 1.27.2 CI/container toolchain. Full-project vet, CI-pinned Staticcheck, complete race-test discovery and reproducible Ent generation pass under Go 1.27.2 without local experiment flags.

The CI race suite also exposed photos-to-PDF tests starting state deadlines while manual events were waiting behind background file-processing writes. Those tests explicitly dispatch manual events before checking conversion and approval states. The 20 ms polling cadence and existing deadlines remain intact, and status failures include runner logs. Both affected photos-to-PDF tests pass in three consecutive repetitions with race detection under Go 1.27.2.

## 1. Backlog Summary Matrix

| ID | Title | Domain | Priority | Effort | Impact |
|---|---|---|---|---|---|
| **ISSUE-01** | Right-Click Context Menu Actions | Web Frontend | High | Medium | Major productivity & UX polish |
| **ISSUE-02** | Workspace Optional Description in Dialogs | Web Frontend | Low | Trivial | Form completeness |
| **ISSUE-03** | Search Input Focus Lost on Keystroke Debounce | Web Frontend | Critical | Trivial | Bug fix (unusable search typing) |
| **ISSUE-04** | Windows Drag-and-Drop Upload Failure | Web Frontend | High | Small | Platform bug fix |
| **ISSUE-05** | Drag-and-Drop Item Navigation to Folders | Web Frontend | Medium | Medium | Direct manipulation UX |
| **ISSUE-06** | Currency Field ISO Presets & Default Search | Web Frontend | Medium | Small | Form usability |
| **ISSUE-07** | Smart Folders UX/UI Complete Rework | Web Frontend | High | Large | High feature adoption & clarity |
| **ISSUE-08** | Search Content Type Filter & Related Items | Web Frontend / API | Medium | Medium | Discovery & graph navigation |
| **ISSUE-09** | Default Versioning Disabled for All Collections | DMS Core | High | Small | Storage savings & user expectation |
| **ISSUE-10** | File Renaming Flow During/After Upload | DMS / Web Frontend | High | Medium | Essential file management |
| **ISSUE-11** | System Fields & Tag Visibility in Definitions | DMS / Web Frontend | Medium | Small | Mental model consistency |
| **ISSUE-24** | Changing Any Field in a List Schema (Deferred) | DMS / Web Frontend / API | Medium | Large | Consistent schema editing across field properties |
| **ISSUE-13** | Unresolved Reference in `publications.revision_id` | DMS Portability | Critical | Small | Fix export blocker |
| **ISSUE-23** | Simplified SQLite Migrations via sqlite3def | Database / DevOps | High | Medium | Eliminates custom AST diffing; seamless binary upgrades |
| **ISSUE-14** | `file.updated` Event (Prevent Redundant Chunks) | Workflow / DMS | High | Medium | Eliminates redundant AI/OCR load |
| **ISSUE-15** | Manual Workflow Execution on Selected Items | Web Frontend | Critical | Medium | Core workflow feature |
| **ISSUE-16** | Trigger Cardinality Contracts (0, 1, or N Items) | Workflow Engine | High | Medium | Authoring contract clarity |
| **ISSUE-17** | Item Details Workflows Tab & Run History | Web Frontend | High | Medium | Item-centric automation |
| **ISSUE-18** | On-Demand (Manual) Built-in Workflow Copies | Workflow Engine | Medium | Small | Operational control |
| **ISSUE-19** | Async Low-Res Blob Previews via Workflow | Workflow / Storage | High | Large | Storage & performance |
| **ISSUE-20** | Admin REST Tokens for External Integrations | Identity / Admin | High | Medium | External scripting & automation |
| **ISSUE-21** | iCal Integration DNS Resolution in Docker NPM | Samples / Network | High | Small | Fixes external integration |
| **ISSUE-22** | Configure NPM Sample with Native Zvec Search | Samples / DevOps | Medium | Small | Production parity sample |
| **ISSUE-25** | Retention for Scheduled Maintenance Executions | Workflow / Storage | High | Small | Bounds database growth while idle |
| **ISSUE-26** | Compact Transactional Step-Result Storage | Database / Workflow | High | Small | Measured 288 KiB sample database reduction |
| **ISSUE-27** | Deduplicate Durable Workflow Payloads | Workflow Engine | High | Medium | Reduces repeated results and workflow definitions |
| **ISSUE-28** | Operational Retention Policies and Cleanup Cadence | Workflow / DMS / Config | Medium | Medium | Bounds high-volume operational records |
| **ISSUE-29** | WAL Allocation and Checkpoint Controls | Database / Operations | Medium | Small | Controls retained journal allocation |
| **ISSUE-30** | Storage Diagnostics and Growth Regression Coverage | Observability / Testing | Medium | Medium | Explains allocation and detects sustained growth |
| **FEAT-01** | Standalone Single Binary Distribution & Samples Update | Architecture / Ops | Very High | Epic | Single binary deployment; OIDC & NPM samples updated to use it |

---

## 2. UI & UX Enhancements

### ISSUE-01: Right-Click Context Menu Actions

#### Problem Statement
Currently, users can only access actions by navigating to dedicated action bars, kebab icon menus, or opening the item drawer. Workspaces and collections in the navigation sidebar, as well as items/folders in table and grid layouts, lack native context menu (`contextmenu` / right-click) interactions.

#### Technical Analysis
- **Sidebar (`web/src/components/shell/sidebar.tsx`)**: Renders `<Link>` elements for workspaces, smart folders, lists, and libraries without context menu handlers.
- **Table & Layouts (`web/src/features/lists/items-table.tsx`, `items-layouts.tsx`)**: Table rows, gallery cards, and board cards only bind `onClick` to select or open items.
- PaperGo already includes `radix-ui` which provides a robust context menu primitive (`ContextMenu` / `@radix-ui/react-context-menu`).

#### Proposed Specification
Implement reusable context menus:
1. **Workspace Context Menu**:
   - Actions: Open, New List/Library, Settings, Manage Members, Tags, Export Package.
2. **List / Library Context Menu**:
   - Actions: Open, New Folder (if enabled), Upload (libraries), Settings, Permissions, Delete.
3. **Item / File Context Menu**:
   - Actions: Open / Details, Download (file), Rename, Move, Run Workflow (1 item), Share / Permissions, Classify to Smart Folder, Delete.
4. **Folder Context Menu**:
   - Actions: Open, Rename, Move, Delete.
5. **Multi-Selection Context Menu**:
   - When right-clicking inside a multi-item selection: Run Workflow (N items), Move Selected, Delete Selected.

#### Acceptance Criteria
- Right-clicking any item, folder, list, library, or workspace displays a styled, keyboard-accessible context menu positioned at the pointer coordinates.
- Selecting an action triggers the exact operation as the corresponding toolbar button.
- If multiple items are selected, right-clicking on any selected row retains the multi-item selection and applies actions in bulk.

---

### ISSUE-02: Workspace Creation Optional Description

#### Problem Statement
When creating a workspace, users expect to provide an optional description explaining the workspace's purpose. The UI modal must explicitly communicate that the description is optional, and quick-create prompts should include this field cleanly.

#### Technical Analysis
- **Backend (`internal/httpapi/api.go:450-467`)**: `POST /v1/workspaces` already accepts `description` up to 2000 runes and persists it on `ent.Resource.Description`.
- **OpenAPI (`api/openapi.json:617-623`)**: Formally declared as optional with default `""`.
- **Frontend Form (`web/src/routes/_app/w/index.tsx`)**: Has `<Textarea id="workspace-description" />`, but the label lacks an "(optional)" indicator, and other entry points (e.g. command palette or quick links) do not consistently prompt for description.

#### Proposed Specification
- Update `web/src/routes/_app/w/index.tsx` label to: `<Label htmlFor="workspace-description">Description <span className="text-muted font-normal text-xs">(optional)</span></Label>`.
- Add a helpful placeholder: `e.g. Client deliverables, finance audits, or shared department files`.
- Ensure any quick-creation action opened from sidebar or command palette surfaces the same dialog.

#### Acceptance Criteria
- Workspace creation dialog displays the Description field labeled with optional indicator.
- Empty description successfully creates the workspace with `""`.
- Non-empty description saves and displays immediately in the workspace header card.

---

### ISSUE-03: Search Input Focus Loss During Active Typing

#### Problem Statement
In lists and libraries, typing a search query with brief pauses causes the input field to lose keyboard focus, interrupting typing and frustrating users.

#### Technical Analysis
- **Location (`web/src/features/lists/collection-page.tsx:385`)**:
  ```tsx
  <SearchBox key={search.q ?? ''} initial={search.q ?? ''} noun={noun} onSearch={(q) => onSearch({ q }, true)} />
  ```
- **Root Cause**: The component uses `key={search.q ?? ''}`. When the user types, the debounced `onSearch` updates the URL query parameter `q`. Because React observes a different `key`, it unmounts the `<SearchBox>` component and remounts a brand-new instance, immediately destroying DOM focus on the `<input>` element!

#### Proposed Specification
1. Remove `key={search.q ?? ''}` from `<SearchBox>`.
2. Inside `SearchBox`, maintain local state synced with `initial` only when `initial` changes externally (or via `useEffect` with reference check), preserving the active `<input>` ref and focus.
3. Add a clear button `(X)` inside the input to allow instant clearing without losing focus.

#### Acceptance Criteria
- Typing a search term with arbitrary typing pauses keeps focus in the search box at all times.
- URL query parameters update reactively without re-mounting the search component.
- Clearing the search input restores list items while preserving search box focus.

---

### ISSUE-04: Windows Drag-and-Drop Upload Failure

#### Problem Statement
Dragging and dropping files into a document library from Windows File Explorer fails to trigger an upload. The drag overlay either does not activate or the dropped file opens directly in the browser tab.

#### Technical Analysis
- **Location (`web/src/features/lists/collection-page.tsx:275, 469-481`)**:
  ```tsx
  const hasFiles = (event: React.DragEvent) => library && canAdd && event.dataTransfer.types.includes('Files');
  ```
- **Root Cause**:
  1. On Chromium and Edge on Windows, `event.dataTransfer.types` can be exposed as lowercase `'files'` or contain vendor mime-types (e.g., `'application/x-moz-file'`). Case-sensitive check `.includes('Files')` evaluates to `false`.
  2. If `hasFiles(event)` returns `false` during `onDragOver`, `event.preventDefault()` is not executed. In HTML5 Drag and Drop, **if `preventDefault()` is not called during `dragover`, the browser will not fire the `drop` event** and will navigate to the file URL.

#### Proposed Specification
Update drag inspection logic:
```tsx
const hasFiles = (event: React.DragEvent) => {
  if (!library || !canAdd || !event.dataTransfer) return false;
  return Array.from(event.dataTransfer.types).some(
    (type) => type.toLowerCase() === 'files' || type === 'application/x-moz-file'
  );
};
```
Ensure `onDragOver` always invokes `event.preventDefault()` and sets `event.dataTransfer.dropEffect = 'copy'` when `hasFiles(event)` is true.

#### Acceptance Criteria
- Dragging one or multiple files from Windows File Explorer into Chrome/Edge on Windows displays the drop indicator.
- Dropping files immediately enqueues them in the upload manager.
- The browser never navigates away to display raw file bytes.

---

### ISSUE-05: Drag-and-Drop Item Navigation to Folders

#### Problem Statement
Users cannot drag items onto folder rows or breadcrumbs to move them into directories; moving currently requires opening the item menu, selecting "Move", and picking a destination from a modal tree.

#### Technical Analysis
- Board layout (`web/src/features/lists/items-layouts.tsx:220`) has `draggable={!readOnly && item.kind === 'item'}` for board cards, but Table layout rows have no HTML5 drag source attributes.
- Folder rows in `ItemsTable` and folder breadcrumb links in the header do not listen to `onDragOver` / `onDrop`.
- Backend supports item relocation: `PATCH /v1/resources/{id}` with `If-Match: {version}` and body `{"parent_id": "{folderId}"}`.

#### Proposed Specification
1. Make item rows in `ItemsTable` draggable when the user has `write` access:
   - `onDragStart` sets transfer data with `application/x-papergo-item: { id, version, kind }`.
2. Folder rows and breadcrumb targets implement drop targets:
   - Visual highlighting on drag enter (`bg-accent-soft border-accent`).
   - `onDrop` calls `moveItem(itemId, targetFolderId)`.
3. Support dragging multiple items if they are currently checked.

#### Acceptance Criteria
- Dragging an item onto a folder row in the table moves the item into the folder.
- Dragging an item onto a breadcrumb folder moves it to the target parent.
- If moving fails (e.g., duplicate filename conflict in library folder), an informative toast notification appears with options to rename or replace.

---

### ISSUE-06: Currency Field Preset Codes and Defaults

#### Problem Statement
When defining a currency column, users are presented with a blank text input to type raw ISO-4217 currency codes (e.g., "USD", "EUR"). If a user does not know the exact 3-letter code, configuration is error-prone. There is also no workspace/organization default currency inherited automatically.

#### Technical Analysis
- **Location (`web/src/features/admin/field-dialog.tsx:281-290`)**:
  ```tsx
  <Input id="field-currency" value={options.currency_code ?? ''} onChange={...} />
  ```
- No searchable currency picker exists in the frontend.
- No default currency fallback is configured on the workspace or organization resource.

#### Proposed Specification
1. Provide a curated standard currency list (USD, EUR, GBP, JPY, CAD, AUD, CHF, CNY, BRL, INR, etc.) with codes, symbols, and localized names.
2. Replace raw `<Input>` with a searchable `<Combobox>`:
   - Filterable by code ("EUR"), symbol ("€"), or name ("Euro").
   - Allows custom 3-letter ISO code entry if an exotic currency is required.
3. Workspace and Organization settings expose `default_currency` (default: "USD" or "EUR"). New currency field definitions pre-select this default.

#### Acceptance Criteria
- Field definition dialog features a searchable combobox with currency symbols, codes, and English names.
- Selecting a currency sets `options.currency_code`.
- Creating a new currency field automatically selects the workspace default currency if configured.

---

### ISSUE-07: Smart Folders UX/UI Complete Rework

#### Problem Statement
The current Smart Folders implementation is unintuitive:
- Creation requires manually typing arbitrary backend database field keys (`field: "status"`) and selecting raw comparison operators (`"eq"`, `"gte"`).
- Navigating a smart folder presents empty or confusing views where users are prompted to "Classify item" manually, blurring the line between dynamic saved searches and static classification tags.
- Non-technical users cannot decipher how to build or benefit from smart folders.

#### Technical Analysis
- **Create Dialog (`web/src/features/smart-folders/create-dialog.tsx`)**: Exposes raw technical keys and separate value-type selectors without field introspection.
- **Folder Page (`web/src/features/smart-folders/folder-page.tsx`)**: Treats smart folders as hierarchical grouped nodes (`definition.group_by`) requiring path navigation before items appear.

#### Proposed Specification
Redesign Smart Folders with two distinct modes and a visual query builder:
1. **Dynamic Smart Queries (Saved Searches)**:
   - Visual Filter Builder: Select from human-friendly dropdowns:
     - Target: Workspace or All Workspaces.
     - Condition: Collection type (Lists, Libraries), Content Type, Modified Date (Past week, Past month, Custom), File Type (PDFs, Images, Documents), or Tags.
     - Custom Metadata Rules: Select a known field name from available fields in the workspace, with typed input controls (dates, dropdowns for choices).
2. **Visual Classification Folders**:
   - Clear rule description: *"Items matching these criteria will automatically appear in this folder."*
   - Live query preview showing how many items currently match the criteria before saving.
3. **Smart Folder Browsing**:
   - Present results using the familiar collection layout (Table, Grid) with standard sorting and item opening.
   - Display a pill tag on each item indicating which collection/library it originates from.

#### Acceptance Criteria
- Creating a smart folder requires zero typing of raw database field keys.
- Filter criteria can be configured using visual rule dropdowns.
- Opening a smart folder displays all matching items directly in a unified view with collection provenance badges.

---

### ISSUE-08: Global Search Content Type Filtering & Relationships Section

#### Problem Statement
1. The global search interface (`/search`) allows filtering by workspace, collection, tag, and kind (`item` vs `folder`), but does not provide a filter for **Content Types** (e.g., Invoices, Contracts, Receipts).
2. Search result cards only show title, excerpt snippet, and collection name; they do not reveal an item's relationships (e.g., "Parent Receipt", "Linked Purchase Orders").

#### Technical Analysis
- **Search Route (`web/src/routes/_app/search.tsx`)**: Queries `/v1/search` with `workspace`, `collection`, `tag`, and `kind`.
- **Backend Search API (`internal/httpapi/search.go`, `internal/search`)**: The query engine already parses `content_type` filters and includes `content_type_id` on indexed items.
- **Relationships (`ent/schema/relationship.go`)**: Relationships link source and target resources via typed definitions.

#### Proposed Specification
1. **Content Type Facet**:
   - Add a "Content Type" filter dropdown in the search header, populated from the workspace/organization content type catalog.
   - Pass `content_type={id}` to search parameters.
2. **Related Items Section on Hit Cards**:
   - On each search hit card, add an expandable or preview section titled "Relationships":
   - Show badges for outgoing/incoming links: e.g., `[Contains receipt line (3 items)]` or `[Linked Contract: ABC-123]`.
   - Clicking a related badge navigates directly to that item or filters search to related resources.

#### Acceptance Criteria
- Search page includes a "Content Type" selector that accurately filters search results.
- Search result items display their direct relationship badges.
- Clicking a relationship badge opens the related item or navigates to its parent collection.

---

## 3. DMS, Content & Storage Lifecycle

### ISSUE-09: Default Versioning History Disabled on Lists and Libraries

#### Problem Statement
Libraries currently default to `versioning: "major"` while lists default to `versioning: "off"`. Users expect all newly created collections (both lists and libraries) to have versioning history disabled by default, preventing unexpected storage growth.

#### Technical Analysis
- **Location (`internal/dms/service.go:227-230`)**:
  ```go
  versioning := "off"
  if in.Kind == "library" {
      versioning = "major"
  }
  ```
- **Ent Schema (`ent/schema/resource.go:43`)**: Defaults to `off`.
- Hardcoded override in `dms/service.go` forces libraries to `"major"`.

#### Proposed Specification
1. In `internal/dms/service.go:227`, retain `versioning := "off"` unconditionally for both lists and libraries unless `in.Versioning` is explicitly provided.
2. In frontend collection creation (`web/src/features/workspaces/new-collection-dialog.tsx`), ensure any versioning toggle defaults to unchecked/off.
3. Collection general settings (`web/src/features/lists/collection-settings.tsx`) clearly show "Versioning: Off" with an opt-in toggle to enable major versioning and configure retention bounds (1–500).

#### Acceptance Criteria
- Newly created libraries have `versioning = "off"` in the database.
- Uploading a new file version to an item in a library with versioning disabled replaces the active file and reclaims previous bytes according to retention policies, rather than creating an immutable historical revision.
- Version history tab explicitly informs the user that versioning is disabled for the collection.

---

### ISSUE-10: File Renaming Flow During and After Upload

#### Problem Statement
When uploading a file, users cannot rename the file before submission. If the file name conflicts with an existing file in the library, the upload fails with a 409 conflict error. Furthermore, renaming an item in a library should synchronize the logical display name and the physical file attachment name where appropriate.

#### Technical Analysis
- **Upload Component (`web/src/features/documents/uploads.tsx`)**: Directly transfers `file.name` to the multipart form endpoint `/v1/resources/{library}/documents` without an opportunity to edit the name.
- **Item Sheet (`web/src/features/lists/item-sheet.tsx:306`)**: Editing `name` in the Details tab updates `Resource.Name`, but the File tab retains the original `version.filename`.
- **Inline Renaming**: Missing from table and card layouts.

#### Proposed Specification
1. **Pre-Upload Conflict Detection / Rename Option**:
   - If an upload encounters a 409 conflict (filename already exists), the upload tray offers two quick actions: **"Rename"** (prompting for a new filename) or **"Replace existing"** (creating a new file version if permissions allow).
2. **Item Renaming in Library**:
   - Provide an explicit "Rename" action in the item context menu and details header.
   - Renaming validates that the target filename preserves or updates the extension properly and checks for sibling name collisions (`name_key`).
   - Updating the item name updates both the resource name and the current revision's attachment display filename.

#### Acceptance Criteria
- Renaming an uploaded document through the context menu or details panel updates the name across table views and the file download header.
- Attempting to rename to an existing sibling name reports a clear inline error without crashing.
- File upload collisions prompt the user to rename immediately in the upload drawer.

---

### ISSUE-11: System Fields and Tag Visibility in Field Definitions

#### Problem Statement
When inspecting field definitions in a fresh library, the system reports "no fields defined" because only custom user-defined schema fields are returned. System fields (Name, Tags, File Size, Created Date, Author) are omitted. In addition, when opening the "Field definitions" popup on an item, tags assigned to that item are not reflected in the schema view.

#### Technical Analysis
- **API (`internal/httpapi/api.go:565-572`)**: `GET /v1/resources/{id}/fields` calls `dms.Fields()` which queries `ent.FieldDefinition` records belonging to that container's content types. System properties (`name`, `tags`, `created_at`, `updated_at`, `created_by`, `blob_size`) are intrinsic attributes of `ent.Resource` and are not modeled as `FieldDefinition` rows.
- **Frontend Schema Dialog (`web/src/features/lists/item-history.tsx:65-110`)**: Only renders `schema.data.definition.fields`, giving the false impression that the document has no attributes.

#### Proposed Specification
1. Update `ItemSchemaView` and collection field inspectors to present a two-tier view:
   - **System Properties**: Name, File Size, Content Type, Created By/At, Modified By/At, and Tags/Keywords.
   - **Custom Fields**: Dynamic schema-defined fields.
2. When displaying the item's schema definition, include the item's assigned tags/keywords in the metadata summary with links to term definitions if applicable.

#### Acceptance Criteria
- "Field definitions" dialog displays built-in system fields with their system types and current values.
- Tags/Keywords are clearly visible as an intrinsic metadata field.
- Custom fields are grouped distinctly from system fields.

---

### ISSUE-24: Changing Any Field in a List Schema

**Status:** Deferred; outside the current implementation phases. **Effort:** Large.

#### Problem Statement
Administrators need a consistent way to edit existing fields in list and library schemas. Field keys, types, decimal scales, multiplicity and reference scopes are currently immutable; labels and supported validation/display settings can already be edited. Editing one additional property in isolation does not provide a complete schema-editing experience.

#### Proposed Specification
1. Design a unified field editor for existing list/library fields, covering names/keys, types, precision/scale, single/multiple values, choices, defaults, validation rules, required/indexed flags and lookup/taxonomy scopes.
2. Define an explicit support matrix for field changes. Preview affected values and dependent views, filters, content types, templates and workflows; explain unsupported changes and require an explicit decision for any destructive change. Do not silently discard values or truncate selections.
3. Define the scope of edits to shared field definitions, including all attached collections, permissions and concurrent schema/item edits. Make that scope visible before applying a change.
4. Validate the complete change before committing. Apply accepted changes to current item values, defaults, schemas and indexes consistently and atomically; a rejected change leaves them untouched.
5. Preserve the schemas paired with published and historical business snapshots. Define restoration and publication behavior when current fields differ, as part of the same design.
6. Update the API contract, item forms, collection schema UI, queries, export/import and documentation together. Follow the single-schema/no-compatibility-code repository rules; do not introduce deployment migrations or upgrade paths.

#### Acceptance Criteria
- Existing fields have one consistent editing flow, with clear rules for every property and field type.
- Choice cardinality changes are evaluated alongside other schema edits rather than implemented as a special exception.
- Invalid or lossy changes cannot silently alter data, including across shared collections.
- Accepted edits keep values, defaults, indexes and dependent features consistent; rejected edits leave no partial changes.
- Published/history snapshots remain interpretable, with specified behavior for restoration and subsequent edits.
- API, UI, permission, concurrency, rollback, query and portability tests cover the agreed support matrix.

---

### ISSUE-13: Workspace Package Export Publications Reference Fix

#### Problem Statement
Exporting a workspace package fails with the error:
`workspace package has an unresolved reference in publications.revision_id`.
This occurs if publishing was enabled and subsequently disabled in a library, or when historical publications reference revisions that are no longer part of the export package. Publishing state should never block workspace export.

#### Technical Analysis
- **Location (`internal/dms/workspacepackage.go:785-790`)**:
  ```go
  if column != "id" && column != "external_id" && strings.HasSuffix(column, "_id") && value != nil ... {
      id, ok := value.(string)
      if !ok || !known[id] {
          return invalid("workspace package has an unresolved reference in " + table + "." + column)
      }
  }
  ```
- In `publications`, `revision_id` is optional (`Optional().Nillable()`). If an item was published, and later unpublished or modified while versioning was off or historical revisions were purged, `publications.revision_id` points to a revision ID not collected in `known`.
- The strict reference validator treats missing publication revision references as a fatal package corruption.

#### Proposed Specification
1. In `workspacepackage.go`:
   - Treat `publications.revision_id` as non-fatal during package graph validation. If the referenced revision is not in `known`, nullify `revision_id` in the exported package row or export the publication snapshot independently.
   - A publication contains an immutable `snapshot` JSON payload that preserves all published data independently of whether the underlying `item_revision` entity still exists.
2. Ensure importing packages gracefully restores publications even if the original revision ID was discarded.

#### Acceptance Criteria
- Toggling publishing on, publishing an item, toggling publishing off, and exporting the workspace succeeds with 200 OK and generates a valid `.zip` package.
- Importing the exported package restores the workspace without foreign key errors.

---

### ISSUE-23: Simplified SQLite Migrations via sqlite3def (sqldef) and Embedded Sequential Upgrades

**Status: Deferred; excluded from the current Phase 4 scope.**

#### Problem Statement
Currently, PaperGo development maintains a single `migrations/20261007000100_schema.sql` file. During releases, an ephemeral `release.json` bundle is generated using a custom release planner (`internal/releaseplan`) that relies on Atlas Go libraries and custom SQL catalog inspection to compute upgrade statements.
This setup has significant drawbacks:
1. The custom schema diff planner is complex, brittle with SQLite table alterations/foreign keys, and requires maintaining custom AST diffing logic.
2. Developers want declarative, reliable schema diffing using standard, battle-tested tools (`sqlite3def`).
3. When a release tag is cut, the release pipeline should calculate the exact idempotent diff between the previous released schema and the current target schema using `sqlite3def`, store the resulting migration script directly into the git repository, and embed all migration steps into the compiled application binary.
4. On application startup, the binary inspects the database's current version (e.g., via `PRAGMA user_version` or `schema_version` table), verifies what migrations are pending, and sequentially applies them inside an atomic transaction until the database schema matches the expected version.

#### Technical Analysis
- **sqlite3def (`sqldef`)**:
  - `sqldef` (specifically `sqlite3def`) is a declarative schema management tool for SQLite. It takes the target schema SQL and the current database (or previous schema SQL) and outputs the minimal, exact DDL required to transform the current schema into the target schema (`--dry-run`).
  - It cleanly handles table creations, column additions, index alterations, and SQLite-compatible table rebuilds.
- **Release Tag Pipeline Workflow**:
  - In CI/CD when a tag `vX.Y.Z` is triggered:
    1. Retrieve previous released tag `vA.B.C` and its target schema snapshot (`schema.sql`).
    2. Run `sqlite3def --dry-run` comparing previous schema to new `schema.sql`.
    3. Generate the migration SQL diff: `migrations/versions/vX.Y.Z.sql` (or structured entry in `migrations/migrations.json`).
    4. Save the generated migration into the repository and commit it before building the release binary.
- **Embedded In-Binary Sequential Migrations**:
  - Using Go `//go:embed versions/*.sql` or `migrations/migrations.json`.
  - On startup in `cmd/api/main.go` / `database.Open`:
    1. Query current database version ($V_{db}$) from the database (`PRAGMA user_version` or `schema_version` table).
    2. If $V_{db} < V_{target}$, identify all sequential migration steps $V_{db} \to \dots \to V_{target}$.
    3. For each step:
       - Open dedicated connection with `PRAGMA foreign_keys = OFF`.
       - Run `BEGIN IMMEDIATE`.
       - Execute statements.
       - Run `PRAGMA foreign_key_check` and `PRAGMA quick_check`.
       - Update recorded version to step version.
       - `COMMIT`.
    4. Database is guaranteed to be at expected version $V_{target}$ before any HTTP handlers or workflow runners start.

#### Proposed Specification
1. **Tooling Integration**:
   - Install `sqlite3def` in development environment and CI release workflows.
   - Replace `internal/releaseplan` Atlas AST diff generator with `sqlite3def` diff generation.
2. **Release Pipeline Script (`scripts/release-migrations.sh` / `scripts/release-metadata.py`)**:
   - On release tag:
     - Diff `previous_schema.sql` and `current_schema.sql` with `sqlite3def`.
     - Output versioned migration file: `migrations/versions/{tag}.sql`.
     - Save diff in git repo prior to compilation.
3. **Application Embedded Engine (`migrations/embed.go`, `internal/database/migrations.go`)**:
   - Embed all migration files via `//go:embed versions/*.sql`.
   - On boot: read current schema version, sequentially execute all unapplied migrations in chronological version order, and verify schema integrity.
4. **Development Workflow**:
   - Developers continue editing the single source schema in development.
   - Migration diffs are generated exclusively at release tag time, keeping the repository clean of messy day-to-day migration thrash while guaranteeing seamless sequential upgrades in production binaries.

#### Acceptance Criteria
- Release pipeline automatically runs `sqlite3def` on version tag, produces the SQL diff between previous and current schema, and commits it to the repository before compiling.
- Compiled binary embeds all historical migration diffs.
- Booting an existing database from an older release version applies all intermediate migrations sequentially in order until reaching the binary's target schema version.
- Fresh database initialization executes the complete current schema in one shot and records the latest version.

---

### ISSUE-25: Retention for Scheduled Maintenance Executions

**Status:** Done. **Priority:** High. **Effort:** Small.

#### Findings
Successful executions of `papergo.tick`, `papergo.search_embed`, `papergo.system-retention` and `papergo.retention` are deleted two hours after they finish, capped by `RUN_RETENTION`. Failures and max-recovery executions stay for `RUN_RETENTION`. Pending and running executions are not selected. Cleanup is paged, deletes DBOS child rows before the status row, and runs from both the hourly system-retention pass and the hourly retention pass. The running pass is not terminal, so it is collected later. Application runs, step markers, waits and staged files stay on `deleteRuns`. Schedule ids are `sched-<name>-<time>` and automatic backfill is off, so a collected tick is not dispatched again after restart. `TestMaintenanceRetentionKeepsActiveWorkAndApplicationRuns` covers an aged page, a pending execution, a failure, a recent success, payload and notification removal, an application run, and a restart.

#### Problem Statement
Scheduled maintenance creates durable DBOS executions while PaperGo is idle, but the application-run retention paths do not collect them. The [sample storage analysis](reports/database-storage-analysis.md) found 55 successful `papergo.tick` executions, four `papergo.search_embed` executions and one `papergo.system-retention` execution within about 55 minutes of recorded activity.

#### Technical Analysis
- `internal/runner/runner.go` schedules ticks every minute, embeddings every 15 minutes, system retention hourly and general retention daily: approximately 1,561 maintenance executions per continuously running day.
- `Runner.Purge` filters DBOS executions to `papergo.run`; `PurgeSystemRuns` selects application runs through `workflow_runs`. Neither path collects these maintenance executions, so `RUN_RETENTION` alone does not bound them.

#### Proposed Specification
1. Add bounded, paginated cleanup for terminal executions of the explicit maintenance workflow names, including their DBOS inputs, outputs and associated runtime records.
2. Retain active/pending executions and enough completed schedule history for scheduler deduplication and diagnostics. Keep application runs on their existing coordinated cleanup path.
3. Make cleanup restart-safe and ensure its own completed executions are eventually collected.

#### Acceptance Criteria
- An idle workload spanning multiple cleanup windows has bounded maintenance execution and payload counts.
- Pending/running executions survive cleanup; completed maintenance executions outside retention are removed without duplicate schedule dispatch after restart.
- Cleanup does not bypass application-run marker, wait, task or staged-file cleanup.

---

### ISSUE-26: Compact Transactional Step-Result Storage

**Status:** Done. **Priority:** High. **Effort:** Small.

#### Findings
`runner_step_results` is an ordinary rowid table. The unique `(run_id, step_id)` key is unchanged, and there is no Ent model for this table. A 419-row workload (300 outputs of 64 bytes, 24 of 900 bytes and 95 of 1,200 bytes; 154,800 payload bytes) allocated 516,096 bytes as `WITHOUT ROWID` and 225,280 bytes as a rowid table plus its primary-key index, measured with `PRAGMA page_count` at a 4,096-byte page. `TestCrashRecovery` still finishes the committed stamp once after the process dies before DBOS records the rest of the run. Concurrent inserts keep one row, lookup returns that output, and delete removes it.

#### Problem Statement
`runner_step_results` uses `WITHOUT ROWID` for variable JSON outputs, causing disproportionate overflow-page allocation. The sample contains 419 rows with 136,537 bytes of output, but the table occupies 496 KiB, including 95 overflow pages. Ordinary `VACUUM` on the sample saved zero bytes.

#### Proposed Specification
1. Use an ordinary rowid table while retaining the unique `(run_id, step_id)` key and current transactional replay semantics. An isolated full-sample copy with this layout and compaction was 288 KiB smaller (7.8% of the database).
2. Edit `migrations/20261007000100_schema.sql` in place, keep Ent metadata consistent where applicable, regenerate Ent and refresh the Atlas checksum. Do not add migrations, compatibility paths or data conversion procedures.
3. Measure table plus primary-key-index allocation on representative step sizes, including outputs around the overflow threshold.

#### Acceptance Criteria
- A reproducible sample-shaped workload demonstrates lower total allocation, including indexes, with identical retained records.
- Recovery after a committed domain write but before DBOS records completion does not repeat the write.
- Concurrent attempts retain one result per run/step, and lookup, retry and retention behavior remain correct.

---

### ISSUE-27: Deduplicate Durable Workflow Payloads

**Status:** Done. **Priority:** High. **Effort:** Medium.

#### Findings
Each transactional step body is stored once, in `runner_step_results` (the rowid table from ISSUE-26). DBOS records `{"$papergo":"step","step":N}`, and the runner reads the body for the workflow that is executing. The load step records the workflow id, version, key, author, system flag and role. The definition is read from that immutable version after the step. A retry copies the reused step bodies onto the new run before the fork exists, and removes those copies when the fork or the run row does not stick. Finished runs delete their own bodies with the run, so a retry keeps the copies it needs.

`TestDurableStepReferences` ran two item-created runs of one version whose definition is 8,384 bytes. The two load markers are identical and total 210 bytes. Both runs' step bodies total 238 bytes. The six durable step outputs stored by DBOS total 240 bytes. `TestCrashRecovery` still finishes the committed stamp once. `TestFailedRunRetriesFromFailedStep` still stamps the item once.

#### Problem Statement
All 419 transactional step outputs in the sample are also stored as exact base64-encoded duplicates in DBOS: 136,537 raw bytes become another 182,372 encoded bytes. Separately, 104 `load` outputs repeat full workflow definitions, totaling 121,404 bytes for only five distinct payloads whose combined size is 3,696 bytes.

#### Technical Analysis
- `internal/runner/steps.go` commits the domain write and replay marker atomically; DBOS separately stores the returned result. Removing the marker would compromise exactly-once writes.
- `internal/workflow/run.go` includes the full parsed definition in every loaded result despite retaining immutable workflow versions.

#### Proposed Specification
1. Store transactional results once and return a small stable reference through DBOS. Resolve references consistently on first execution, replay and retry.
2. Replace repeated loaded definitions with retained immutable workflow-version references while preserving the exact definition, author and execution context selected for the run.
3. Coordinate reference lifetime with run retention, retry, cancellation and account deletion. Retain referenced payloads and definitions for active or waiting consumers.
4. Implement one current representation and update callers, tests and documentation together. Measure combined savings with ISSUE-26 rather than adding overlapping estimates.

#### Acceptance Criteria
- DBOS transactional-step outputs no longer duplicate full result bodies.
- Repeated runs of one workflow version do not persist full definitions per run.
- Crash recovery, replay, retry and long waits produce the same results without repeating committed domain writes.
- Cleanup cannot leave dangling references or delete payloads still needed by active consumers.

---

### ISSUE-28: Operational Retention Policies and Cleanup Cadence

**Status:** Done. **Priority:** Medium. **Effort:** Medium.

#### Findings
`SYSTEM_RUN_RETENTION` defaults to 24h for successful automatic system runs. `RUN_RETENTION` remains the ceiling, because the application retention pass collects every finished application run. Failures and manual system runs use `RUN_RETENTION`. `RESULT_CACHE_TTL` defaults to 720h and is parsed on its own: `RUN_RETENTION=2h` leaves the cache at 720h and system retention at 24h. Explicit values of 3h and 48h stay beside that 2h run retention. A 30-minute cache lifetime is rejected.

`papergo.system-retention` runs hourly at minute 15 and `papergo.retention` hourly at minute 45, so an eligible row can wait at most one hour. Dispatched events, expired activity records, live changes and terminal operations are deleted in bounded pages. `TestDispatchedEventsDeleteInPages` clears a dispatched set larger than the page. `TestPurgeLeavesAuditAndPublications` runs a purge with audit rows and a publication present and both counts stay the same. History limits still keep the published snapshot when user-visible history is off.

#### Problem Statement
Operational records dominate storage after short usage, but retention settings and cleanup schedules do not distinguish all workloads. The sample has 104 application runs, 395 domain events and 523 live notifications. Search indexing accounts for 91 runs. `RUN_RETENTION` defaults to 720h, successful system built-ins retain up to 24h, and general cleanup runs daily.

#### Proposed Specification
1. Define and configure retention for successful high-volume system runs separately from failures and user workflow runs. Preserve active/waiting runs and records needed for replay or diagnostics.
2. Align cleanup frequency with configured lifetimes, use bounded batches and collect eligible dispatched events and expired durable activity records consistently.
3. Separate reusable answer-cache lifetime from run retention where their requirements differ; document effective defaults and maximum cleanup delay.
4. Document the distinct lifetimes of runs, events, live/delta records, caches, file history, audit events and publication snapshots. Explicitly state that audit/publication retention is a separate product policy; do not implicitly delete these records or weaken their schema guards.

#### Acceptance Criteria
- Retention boundaries, status-specific policies and cleanup delays are validated and documented.
- Expired operational records are collected within the documented delay without breaking active workflows or retained business snapshots.
- Shortening run retention does not silently alter an independently configured cache lifetime.
- Calendar/publication activity is accounted for even when user-visible item history is disabled.

---

### ISSUE-29: WAL Allocation and Checkpoint Controls

**Status:** Done. **Priority:** Medium. **Effort:** Small.

#### Findings
The shared DSN sets `wal_autocheckpoint=256` and `journal_size_limit=1048576` on every connection from `database.Open`, from a second handle opened with `database.DSN` (the runner and the SQLite search store), and again after the file is closed and reopened. `journal_mode` stays `wal` and `synchronous` stays `FULL`.

One local comparison wrote 700 rows of 3000-byte payloads in a single transaction on a 4096-byte page, ran a passive checkpoint, then inserted one more row. With the policy, the write took 40.0 ms, the passive checkpoint 0.60 ms, and the follow-up 0.79 ms. The journal was 2,900,512 bytes after the large commit and 1,048,576 bytes after the follow-up; 704 frames were copied. The same write with the default 1000-page autocheckpoint and no journal size limit took 32.8 ms, its passive checkpoint 6.81 ms, and its follow-up 0.14 ms, and the journal stayed at 2,900,512 bytes. These timings are one run on this machine.

A 500-row transaction with a 32 KiB page cache grew the journal to 2,055,912 bytes before commit. A reader that started earlier kept the passive checkpoint incomplete and the journal at 2,080,632 bytes. That reader still saw only the seed row, and the committed rows stayed readable on another connection. After the reader finished, the passive checkpoint completed and left the file at 2,080,632 bytes. The next insert truncated it to 1,048,576 bytes and the seed row was still present. The passive read does not delete the journal or discard committed frames.

`GET /v1/storage/status` (`organization.manage`) runs that passive checkpoint and reports busy, log, checkpointed, incomplete, journal bytes, the size limit, the autocheckpoint, journal mode, and synchronous. `PRAGMA wal_checkpoint(TRUNCATE)` during a quiet period reclaims the journal file and leaves the main database file as it is. `VACUUM` rewrites the main database, and PaperGo does not schedule it.

#### Problem Statement
A WAL near 4 MB is consistent with SQLite's default 1,000-page automatic checkpoint threshold and retained journal reuse. PaperGo needs an explicit, measurable allocation policy so operators can control journal overhead and distinguish normal reuse from blocked checkpoints. The supplied sample did not include its live WAL, so checkpoint starvation is not established.

#### Proposed Specification
1. Benchmark `wal_autocheckpoint=256` and `journal_size_limit=1048576` as a starting point for roughly 1 MiB retained allocation, comparing write latency and checkpoint I/O with current defaults.
2. Apply the chosen settings to every relevant connection through the shared DSN used by the API, runner and SQLite search store. Preserve WAL mode and FULL durability.
3. Expose checkpoint progress, incomplete checkpoints and retained journal size through operational diagnostics. Document that the retained size limit is not a hard ceiling during large transactions or blocked checkpoints.
4. Document quiet-period `wal_checkpoint(TRUNCATE)` maintenance and distinguish journal reclamation from main-database compaction. Do not use frequent full `VACUUM` as a substitute for retention or correct table layout.

#### Acceptance Criteria
- Multiple connections and restarts consistently apply the selected settings.
- Completed checkpoints and journal resets bound retained allocation under a representative workload, with measured latency/I/O tradeoffs.
- Long readers and large transactions have regression coverage; incomplete checkpoints are observable and never cause journal deletion or loss of committed data.

---

### ISSUE-30: Storage Diagnostics and Growth Regression Coverage

**Status:** Done. **Priority:** Medium. **Effort:** Medium.

#### Findings
`GET /v1/storage/status` (`organization.manage`) runs a passive checkpoint and then reports main-file allocation. `dbstat` lists pages owned by a table or index and omits freelist pages. Accounted bytes plus freelist bytes equal `page_count * page_size`. Unused bytes are slack inside named pages. The route does not delete rows and does not call `wal_checkpoint(TRUNCATE)`.

A probe that inserted 200 rows of 500 bytes and deleted 100 reconciled to a 110,592-byte file (27 pages): `dbstat` accounted 61,440 bytes, the freelist held 49,152 bytes (12 pages), and named pages contained 10,049 bytes of slack. The same totals held after the file was closed and reopened.

A fresh API database was 1,298,432 bytes across 317 pages, with 1,168,059 bytes of slack and no freelist pages. Its SQLite search file was 238,968 bytes and held no documents. Two blob rows that shared one SHA-256 and a 2,000,000-byte size counted once as blob bytes, and the database file stayed smaller than that content. File bytes stay in the object store.

The same response counts workflow runs from `workflow_status` name and status when that table exists, and otherwise from `workflow_runs` event type and live status, without reading payload columns. Expired counts use each row's `expires_at` for caches and activity records, seven days for live changes, and 30 days for terminal operations and dispatched events. Audit events and publications are reported separately because cleanup leaves them. Orphan `runner_step_results` are counted, not deleted.

A soak planted an expired cache row, an eight-day live change, and an orphan step result. After the cache purge, live-change collection, and deletion of that orphan, a second and third status stayed at zero for those counts and the audit count was unchanged. A reader that had selected from `live_changes` made the following checkpoint incomplete with a non-zero journal; after that reader finished, the next checkpoint completed. An unchanged calendar refresh, including a not-modified download, added no item revision, publication, workflow run, or `item.updated` event. Changing the summary added a revision and an `item.updated` event and left the publication and run counts unchanged.

#### Problem Statement
File count alone does not explain storage growth. The analyzed database is 3.61 MiB, with a 1.43 MiB empty-schema baseline, 57 calendar items and three file-backed items. It has zero freelist pages despite substantial spare bytes inside allocated pages. Operators need a breakdown that separates fixed schema overhead, retained data, indexes, journals and external file/search storage.

#### Proposed Specification
1. Provide storage diagnostics for main database, WAL, search and blob bytes; page/freelist counts; table/index allocation; run counts by name/status; expired records; and cleanup/checkpoint outcomes. Exclude document payloads and credentials from diagnostic output.
2. Add a reproducible workload covering idle scheduling, initial calendar import, unchanged calendar refresh, changed calendar items, uploads and repeated processing.
3. Verify that unchanged calendar refreshes create no new item revisions, publications or search runs. The sample's 84 calendar publication snapshots across 57 calendar items justify coverage, but do not prove redundant refresh writes.
4. Measure growth across retention windows and restarts. Report bounded operational allocation separately from intentionally retained content, audit and publication growth.

#### Acceptance Criteria
- Diagnostics reconcile SQLite page allocation with per-table/index totals and distinguish internal page slack from freelist pages.
- Idle operational record counts stabilize after retention; unchanged calendar refreshes produce no content/workflow fan-out.
- A soak workload detects missing cleanup, growing orphan payloads and stalled checkpoints without requiring the private sample database in automated tests.
- Storage documentation explains baseline overhead and why database size cannot be extrapolated from the number of uploaded files.

---

## 4. Workflow Engine & Automation

### ISSUE-14: Granular `file.updated` vs `item.updated` Events

#### Problem Statement
Whenever an item's metadata (e.g. a tag, description, or custom field) is edited, the server emits `item.updated`. Workflows subscribed to `item.updated` (such as full-text chunking, OCR processing, and vector embeddings) re-execute automatically. Re-reading, OCR-ing, chunking, and embedding multi-megabyte files when only a metadata status changed is computationally wasteful and drives up AI API costs.

#### Technical Analysis
- **Domain Events (`internal/dms/events.go:15-24`)**:
  - `item.created`
  - `item.updated`
  - `file.added`
- There is currently no distinct event emitted specifically when an item's **file content** changes on an existing item (e.g., file replacement).
- Workflows (like `search.enrich`) currently trigger on `item.updated`, running expensive readers and embedders on every metadata edit.

#### Proposed Specification
1. Introduce **`file.updated`** domain event:
   - Emitted when an existing item receives a new file attachment (file replacement/restore).
   - Event payload includes `resource_id`, `blob_id`, `filename`, `size`, `source`, and `previous_blob_id`.
2. Update workflow definitions:
   - Chunking, OCR, and AI content analysis workflows trigger only on `file.added` and `file.updated`.
   - `item.updated` triggers only lightweight metadata indexing workflows (e.g. updating database keyword tables and search filter fields) without touching document blobs.

#### Acceptance Criteria
- Editing an item's tag or title emits `item.updated` and does not trigger blob re-chunking or LLM batch re-analysis.
- Replacing an item's file attachment emits `file.updated` and triggers content processing workflows.

---

### ISSUE-15: Manual Workflow Execution on Selected Item(s)

#### Problem Statement
Users cannot select one or more items in a list or library and trigger a workflow manually from the user interface.

#### Technical Analysis
- **Backend API (`internal/httpapi/workflows.go`, `internal/workflow/store.go:514-555`)**:
  - The engine fully supports starting manual workflows with items: `POST /v1/workflows/{id}/start` with payload `{"items": ["item-1", "item-2"], "inputs": {...}}`.
- **Frontend**:
  - No action button exists in the toolbar or context menu to launch workflows against selected items.
  - No launch dialog exists to present input forms (`input_schema`) for manual execution.

#### Proposed Specification
1. **Toolbar & Context Menu Action**:
   - When items are selected in a list or library, add a **"Run workflow"** action dropdown.
   - The dropdown queries workflows in the workspace with manual triggers matching the collection and selection size.
2. **Launch Modal**:
   - If the selected workflow specifies an `input_schema`, display a generated form dialog using `SchemaForm` (`web/src/features/workflows/schema-form.tsx`).
   - If no inputs are required, prompt for confirmation and start immediately.
3. Show progress toast with a direct link to view the run in the runs dashboard.

#### Acceptance Criteria
- Selecting 1 or N items in a library reveals the "Run workflow" action.
- Selecting a workflow opens its launch parameter form if inputs are defined.
- Submitting starts the workflow and triggers execution on the selected items.

---

### ISSUE-16: Workflow Authoring Item-Cardinality Trigger Configuration

#### Problem Statement
When a workflow developer creates a manual trigger, there is no intuitive UI control to define whether the workflow operates without any items (standalone/item-free), on exactly one item, or across multiple items.

#### Technical Analysis
- **Trigger Schema (`internal/workflow/spec.go:96-99, 166-169`)**:
  - Standalone: `type: "manual"`, no `collection_id`.
  - Single item: `type: "manual"`, `collection_id: "..."`, `selection: "per_item"` (one run per item).
  - Multi-item batch: `type: "manual"`, `collection_id: "..."`, `selection: "selection"` (one run for all items, passed via `{run:items}`).
- **Editor (`web/src/features/workflows/workflow-editor.tsx`)**:
  - Trigger configuration lacks explicit radio controls for item cardinality.

#### Proposed Specification
In the workflow visual editor under Manual Trigger settings:
1. Provide an **Item Association** selector:
   - **None (Standalone)**: Workflow runs independently without selecting items (e.g. daily reports, maintenance).
   - **Single Item**: Workflow runs against one selected item at a time (e.g. Generate Document Summary).
   - **Multiple Items**: Workflow accepts an arbitrary selection of items processed together (e.g. Batch Invoice Approval, Merge PDFs).
2. If Single or Multiple is chosen, allow scoping to:
   - Any collection in workspace, or
   - A specific List / Library.
3. If Multiple Items is chosen, provide hints on using `{run:items}` and `forEach` loops.

#### Acceptance Criteria
- Workflow editor saves the valid trigger JSON corresponding to the chosen cardinality.
- Triggers marked for "Single Item" only appear in the UI when exactly 1 item is selected.
- Triggers marked for "Multiple Items" appear when 1 or more items are selected.

---

### ISSUE-17: Item Details Workflows Panel & Execution History

#### Problem Statement
The Item Details Sheet (`item-sheet.tsx`) has tabs for Details, File, Versions, and Publish, but lacks visibility into automation. Users cannot view workflow runs that have processed the item, nor can they launch applicable workflows directly from the sheet.

#### Technical Analysis
- Runs API provides `GET /v1/workspaces/{id}/workflow-runs?item_id={itemId}`.
- Currently, users must navigate to Workspace Settings -> Workflow Runs and manually search by ID.

#### Proposed Specification
1. Add a **"Workflows"** tab to `item-sheet.tsx`:
2. **Section 1: Run Workflow**:
   - List all available manual workflows applicable to this item.
   - Clicking a workflow opens its input form and launches it directly for this item.
3. **Section 2: Workflow History**:
   - Display a chronological list of recent workflow runs for this item.
   - Show status badges (Running, Completed, Failed, Waiting), trigger type, execution time, and actor.
   - Provide direct links to view node execution details or retry failed runs.

#### Acceptance Criteria
- Item sheet includes a "Workflows" tab.
- Users can run a manual workflow on the open item directly from the tab.
- Past and in-progress runs for the open item are displayed in real time.

---

### ISSUE-18: On-Demand (Manual) Built-in Workflow Variants

#### Problem Statement
Built-in workflows (e.g. OCR image text, summarize document, compose PDF) are configured by default to trigger automatically on events like `file.added`. In many organizations, users prefer to run these operations on-demand to manage compute quotas and verify inputs before processing.

#### Technical Analysis
- **Built-in Definitions (`internal/workflow/builtins.go`)**:
  - Hardcodes automatic triggers: `{"type": "file.added", "collection_id": "..."}`.
- Setting `PUT /v1/workspaces/{id}/workflow-builtins/{key}` allows copying and toggling, but defaults always couple to event subscriptions.

#### Proposed Specification
1. Register dual built-in templates for major system workflows:
   - Automatic variant: e.g. `ocr.automatic` (triggers on `file.added`).
   - On-Demand variant: e.g. `ocr.on_demand` (triggers on `manual` with `selection: "per_item"`).
2. Workspace administrators can enable the on-demand variant without activating the automatic event listener.

#### Acceptance Criteria
- Admin can deploy an on-demand OCR or AI enrichment workflow that never runs on upload, but is immediately available in the item's "Run workflow" menu.

---

### ISSUE-19: Asynchronous Low-Resolution Blob Preview Pipeline

#### Problem Statement
Currently, document viewing and thumbnail displays either download full-resolution original blobs (which can be tens or hundreds of megabytes) or have no preview at all. The UI should display low-resolution previews generated asynchronously in the background.

#### Technical Analysis
- Large PDF/TIFF/JPEG files consume extensive client bandwidth and memory.
- PaperGo has high-performance native imaging tools (`internal/imaging`, `internal/pdf`, PDFium, WebP).
- Built-in workflow mechanism can process uploaded blobs in a background task and store low-res previews in a dedicated cache directory (`data/previews/{hash}.webp`).

#### Proposed Specification
1. **Built-in Preview Generator Workflow**:
   - Triggers on `file.added` and `file.updated`.
   - Uses native PDFium / imaging to render page 1 (or images) into a bounded WebP (e.g. max dimension 800px, quality 75).
   - Saves output to preview storage and attaches preview metadata to the item.
2. **UI Integration**:
   - Before preview generation completes, the UI shows a clean skeleton/placeholder ("Generating preview...").
   - Once generated, cards and item sheets load the low-res preview endpoint `/v1/items/{id}/preview`.
   - Full-resolution bytes are only downloaded when the user explicitly clicks "Download Original".

#### Acceptance Criteria
- Uploading a 20MB image or multi-page PDF generates a low-res WebP preview under 100KB within seconds.
- The UI displays the lightweight preview instantly without loading the full blob.
- While processing, a clear non-blocking placeholder is shown.

---

## 5. Integrations, Identity & Samples

### ISSUE-20: Admin-Managed REST Access Tokens for External Integrations

#### Problem Statement
In PaperDotNet, administrators can generate system-level API access tokens from the Administration console for external integrations, CLI tooling, and webhooks. In PaperGo, token creation is currently limited to personal user tokens in user preferences (`/settings/tokens`), which cannot represent system service accounts.

#### Technical Analysis
- **Schema (`ent/schema/identityresttoken.go`)**: Already includes `subject`, `name`, `prefix`, `secret_hash`, `scopes`, `expires_at`, `revoked_at`.
- **API (`internal/httpapi/identity.go:14-25`)**: Currently mounted under `/v1/identity/me/tokens` bound to `subject(r)`.
- Administration interface has no screen to inspect, issue, or revoke organization-wide service tokens.

#### Proposed Specification
1. **API Endpoints**:
   - `GET /v1/identity/tokens`: Lists all organization tokens (requires `organization.manage`).
   - `POST /v1/identity/tokens`: Generates a new integration token with assigned scopes and service identity.
   - `DELETE /v1/identity/tokens/{id}`: Revokes an integration token.
2. **Administration UI**:
   - Add "Access Tokens" under the Administration navigation.
   - Interface to generate tokens with name, expiration duration (30 days, 90 days, 1 year, Never), and selectable scopes (`workspace.read`, `list.write`, `search.read`, etc.).
   - Secure one-time display modal showing the generated secret token (`pgo_...`).

#### Acceptance Criteria
- Administrators can create named integration tokens with defined scopes from the Administration menu.
- External API calls sending `Authorization: Bearer pgo_...` authenticate successfully with the configured scopes.
- Administrators can view last-used timestamps and revoke tokens immediately.

---

### ISSUE-21: NPM Docker Compose iCal Integration DNS Resolution

#### Problem Statement
When configuring an external Google Calendar iCal feed in the NPM sample (`samples/npm`), the system fails with:
`calendar destination could not be resolved`.

#### Technical Analysis
- **Location (`internal/calendar/fetch.go:71-82`)**:
  ```go
  ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
  if err != nil || len(ips) == 0 {
      return nil, errors.New("calendar destination could not be resolved")
  }
  ```
- **Root Cause (`samples/npm/compose.yaml:65-67, 121-122`)**:
  ```yaml
  networks:
    backend:
      ipv4_address: ${PAPERGO_PRIVATE_IP}
  ...
  networks:
    backend:
      internal: true
  ```
  In Docker Compose, `internal: true` creates an isolated network bridge that **strictly prohibits outbound external traffic and disables external DNS resolution**. The container cannot reach public DNS to resolve `calendar.google.com` or connect to outbound HTTPS.

#### Proposed Specification
1. In `samples/npm/compose.yaml`:
   - Either remove `internal: true` from the backend network or add an egress network gateway for PaperGo.
   - Alternatively, configure explicit Docker DNS resolvers or route external calendar/AI API egress through the reverse proxy.
2. In `samples/npm/setup.py`, document and verify outbound internet access requirements during deployment setup.

#### Acceptance Criteria
- Adding the Google Calendar iCal URL in the NPM sample resolves DNS, validates the URL, and synchronizes calendar events into the target list.

---

### ISSUE-22: NPM Sample Native Vector Search with Zvec

#### Problem Statement
The NPM sample deployment defaults to SQLite full-text search. It should demonstrate PaperGo's native vector and hybrid search capabilities by configuring the built-in `zvec` search engine out of the box.

#### Technical Analysis
- **Config (`internal/config/config.go:102-115`)**:
  - `SEARCH_STORE=zvec`
  - `SEARCH_ZVEC_PATH=/data/search/zvec`
  - `SEARCH_ZVEC_KEYWORD_CANDIDATES=10000`
- `samples/npm/compose.yaml` currently omits these variables, falling back to basic SQLite.

#### Proposed Specification
1. Update `samples/npm/compose.yaml` under `services.papergo.environment`:
   ```yaml
   SEARCH_STORE: zvec
   SEARCH_ZVEC_PATH: /data/search/zvec
   ```
2. Update volume initialization scripts to ensure `/data/search/zvec` permissions are configured properly during container startup.
3. Update `samples/npm/setup.py` verification routines to assert `zvec` is active.

#### Acceptance Criteria
- Running `docker compose up` in `samples/npm` starts PaperGo with `SEARCH_STORE=zvec`.
- Search operations utilize native `zvec` vector and keyword candidate indices.

---

## 6. Major Epic: Standalone Single Binary Distribution

### FEAT-01: Zero-Dependency Single Binary Executable (Linux & Windows)

**Status: Deferred; excluded from the current Phase 4 scope.**

#### Executive Overview
Transform PaperGo into a single self-contained executable for Linux (`papergo`) and Windows (`papergo.exe`). The binary must run immediately without requiring Docker, external databases, separate Node.js runtimes, or external identity providers.

```
+-----------------------------------------------------------------+
|                    PaperGo Standalone Binary                   |
|                                                                 |
|  +-----------------------+           +-----------------------+  |
|  |   Embedded Web UI     |           |   Built-in HTTP/API   |  |
|  |  (go:embed web/dist)  | --------> |   (Local Basic Auth)  |  |
|  +-----------------------+           +-----------------------+  |
|                                                  |              |
|  +-----------------------+           +-----------------------+  |
|  |  Native Zvec Engine   |           |    Local SQLite DB    |  |
|  |   (Embedded CGO)      |           |     (WAL Mode)        |  |
|  +-----------------------+           +-----------------------+  |
|                                                  |              |
|  +-----------------------------------------------------------+  |
|  |            Configuration via config.yaml                   |  |
|  +-----------------------------------------------------------+  |
+-----------------------------------------------------------------+
```

#### Detailed Architecture & Requirements

1. **Embedded Web Frontend**:
   - Build frontend assets (`npm run build --prefix web`) generating static distribution in `web/dist`.
   - Embed into Go executable via `//go:embed all:dist` in `web/web.go`.
   - Serve static assets directly from memory with SPA HTML5 fallback routing.

2. **Configuration File (`config.yaml`)**:
   - Provide clean YAML configuration as an alternative to environment variables:
     ```yaml
     server:
       port: 8080
       bind: 0.0.0.0
       data_dir: ./data
       public_url: http://localhost:8080

     auth:
       mode: local
       # Default initial credentials on first launch
       admin_user: admin
       admin_password: admin

     search:
       store: zvec
       zvec_path: ./data/search/zvec

     storage:
       blob_path: ./data/blobs
       max_upload_mb: 100
     ```

3. **Built-in Local Authentication**:
   - Implement `AUTH_MODE=local`:
   - On initial startup, if no admin account exists, create `admin` with credentials specified in `config.yaml` or prompt securely.
   - Support standard HTTP Basic Auth and Session Cookies for browser navigation without requiring an external OAuth2/OIDC identity provider.

4. **Default Native Vector Store (`zvec`)**:
   - Bundle pre-compiled native `zvec` and ONNX Runtime libraries.
   - Configure `zvec` as default search provider on first boot.

5. **Cross-Platform Compilation & Distribution**:
   - Linux: One executable containing the native application, libraries and OCR models, extracted automatically into a private runtime cache.
   - Windows: One `.exe` containing the native application, DLLs, compiler runtimes and OCR models, built with MSYS2 / MinGW-w64.
   - Releases packaged as `.tar.gz` and `.zip` containing only the binary, sample `config.yaml`, and README.

6. **Modernize OIDC (Authentik) & NPM Samples to Use Single Binary**:
   - Update both reference deployment packages (`samples/authentik` and `samples/npm`) to utilize the unified single binary executable instead of multi-step container builds.
   - **Eliminate Separate Migration Containers**:
     - Remove the separate `initialize` services that previously invoked `[/papergo-admin] command: [initialize, --schema, /schema]`. Because the single binary executes self-contained startup schema checks and embedded migrations automatically on boot, auxiliary migration containers are completely redundant.
   - **Simplified Compose & Configuration**:
     - Modernize `compose.yaml` in both samples to mount a clean `config.yaml` (or set concise flags) rather than managing sprawling lists of environment variables.
     - `samples/npm`: Configured with `auth.mode = proxy`, trusted proxy CIDRs, and `search.store = zvec`.
     - `samples/authentik`: Configured with `auth.mode = oidc`, provider issuer, client ID, and audience.
   - **Sample Setup & Verification Updates**:
     - Update `samples/npm/setup.py`, `samples/npm/verify.py`, `samples/authentik/setup.py`, and `samples/authentik/verify.py` to target and assert single binary behavior and remove obsolete `/papergo-admin` invocations.

#### Acceptance Criteria
- User downloads single binary (`papergo` / `papergo.exe`), runs `./papergo`, and navigates to `http://localhost:8080`.
- Web UI mounts immediately from embedded assets.
- Logging in with `admin:admin` grants full administrative access.
- Uploading documents and executing searches uses native `zvec` out of the box without any third-party infrastructure.
- Both `samples/npm` and `samples/authentik` (OIDC) Docker Compose environments run exclusively using the single application binary, with no separate `initialize` migration containers.
- Setup and end-to-end verification scripts (`setup.py`, `verify.py`) pass cleanly across both samples using the single binary deployment.

---

## 7. Implementation Roadmap

The backlog items are prioritized to maximize user impact and architectural cohesion:

```mermaid
graph TD
    subgraph Phase 1: Critical Bug Fixes
        A1[ISSUE-03: Search Focus Loss] --> A2[ISSUE-04: Windows Drag-and-Drop]
        A2 --> A3[ISSUE-13: Workspace Export Reference Fix]
        A3 --> A4[ISSUE-09: Default Versioning Disabled]
    end

    subgraph Phase 2: Workflow & Core DMS Enhancements
        B1[ISSUE-14: file.updated Event] --> B2[ISSUE-15: Manual Workflow Execution]
        B2 --> B3[ISSUE-16: Trigger Cardinality]
        B3 --> B4[ISSUE-17: Item Workflows Tab]
        B4 --> B5[ISSUE-10: File Renaming Flow]
    end

    subgraph Phase 3: UI/UX & Navigation Polish
        C1[ISSUE-01: Right-Click Context Menus] --> C2[ISSUE-05: Drag-to-Folder Navigation]
        C2 --> C3[ISSUE-06: Currency Field Presets]
        C3 --> C4[ISSUE-11: System Fields in Definitions]
        C4 --> C5[ISSUE-08: Search Content Types & Links]
        C5 --> C6[ISSUE-07: Smart Folders Overhaul]
    end

    subgraph Phase 4: Integrations & Distribution
        D1[ISSUE-21: Docker DNS Resolution Fix] --> D2[ISSUE-22: NPM Zvec Integration]
        D2 --> D3[ISSUE-20: Admin REST Tokens]
        D3 --> D4[ISSUE-19: Async Low-Res Previews]
    end

    subgraph Deferred
        D5[ISSUE-23: sqlite3def Migrations]
        D6[FEAT-01: Standalone Single Binary Executable]
    end

    subgraph Phase 5: Storage Growth and Database Efficiency
        E1[ISSUE-25: Maintenance Retention] --> E2[ISSUE-26: Compact Step Storage]
        E2 --> E3[ISSUE-27: Durable Payload Deduplication]
        E3 --> E4[ISSUE-28: Retention Policies]
        E4 --> E5[ISSUE-29: WAL Controls]
        E5 --> E6[ISSUE-30: Storage Diagnostics and Growth Coverage]
    end

    Phase 1 --> Phase 2
    Phase 2 --> Phase 3
    Phase 3 --> Phase 4
    Phase 4 --> Phase 5
```

### Phase Summary:
1. **Phase 1 (Immediate Stability)**: Fix search keystroke focus loss, Windows file drop failure, export blocker on publication references, and ensure libraries default to versioning off.
2. **Phase 2 (Automation Core)**: Introduce `file.updated` to prevent redundant AI re-chunking, enable manual workflow execution on selected items, add trigger cardinality contracts and item workflow history, and support file renaming and upload conflict resolution.
3. **Phase 3 (Productivity & UX)**: Deliver right-click context menus, drag-and-drop folder moves, currency combobox presets, smart folders complete rework, and relationship discovery in search.
4. **Phase 4 (Integrations & Distribution)**: Deliver Docker network egress, native zvec in the NPM sample, admin integration tokens and asynchronous previews. ISSUE-23 (`sqlite3def` migrations) and FEAT-01 (standalone single-binary distribution) are deferred.
5. **Phase 5 (Storage Growth & Database Efficiency)**: Bound maintenance execution retention (ISSUE-25), compact step-result storage (ISSUE-26), reduce repeated durable payloads (ISSUE-27), tune operational retention and cleanup cadence (ISSUE-28), control retained WAL allocation (ISSUE-29), and establish storage diagnostics and growth regression coverage (ISSUE-30). Add measurement coverage alongside each change and complete the combined soak workload in ISSUE-30. Evidence and measured copy experiments are recorded in the [sample storage analysis](reports/database-storage-analysis.md).

**Deferred (unscheduled): ISSUE-24 — Changing Any Field in a List Schema.** Design general schema editing across field types and properties before implementing individual identity changes.
