# Copying a workspace between PaperGo instances

A workspace package reproduces a shared workspace on another PaperGo instance.
It carries the workspace's name, description, membership and permissions; lists
and libraries with their settings; content types, fields and frozen schemas;
workspace schema templates; list views; shared smart folders; taxonomy and
relationship policies; workflow definitions, versions and triggers; and group
inbox bindings. Shared dependencies are selected from the objects the workspace
uses. PaperGo uses a ZIP containing `manifest.json` and deduplicated `objects/`
entries, with an explicit organization or workspace scope.

Choose whether to include content. With content enabled, the package also carries
items and folders, typed values, unique permissions, retained revisions and
history, publications, relationships, every file version and extracted file text.
Without content, lists and libraries are empty but keep their configuration.
Publication snapshots remain portable after their content revisions are collected.
The package clears unavailable publication revision links and imports the frozen
snapshot independently, using its schema to remap typed references.

## In the browser

1. As an organization administrator, open workspace settings and choose
   **Export this workspace**, or choose a workspace under administration's
   **Export and import** page.
2. Select **Include workspace data and files** as needed and start the export.
3. Wait for the operation to succeed and download its package.
4. On the target instance, open **Export and import**, select the ZIP and
   optionally enter a new workspace name.
5. Choose **Try it** to validate the package. Review the operation's errors and
   warnings. Then choose **Import** and open the created workspace from the result.

The target can already contain other workspaces. An import creates a new workspace;
it does not merge into an existing workspace. A live workspace with the same name
causes a conflict. Supply another name for another independent copy. Repeating
an import with the same name fails without duplicating or changing resources.

## API

Both export and import require `organization.manage`. Export additionally checks
Manage access to the selected workspace when queued and when executed. Package
operations are durable: poll the URL in the response's `Location` header until
`status` is `succeeded` or `failed`.

```http
POST /v1/portability/exports
Content-Type: application/json

{"workspace_id":"<source-workspace-id>","include_data":false}
```

A successful export operation's `result.package_url` downloads the ZIP. Only its
initiator can download it. Packages expire after seven days.

```http
POST /v1/portability/imports?dry_run=true&workspace_name=Finance%20copy
Content-Type: application/zip

<ZIP bytes>
```

Inspect `result.valid`, `errors` and `warnings`, then submit the same package with
`dry_run=false`. A successful workspace import returns `result.workspace_id` and
`result.workspace_name`. Validation uses the same database constraints as the real
import, rolls back metadata, and copies no file objects. The real import applies
all metadata atomically and rebuilds typed/search indexes. Import does not replay
item creation events or historical workflow runs. Schedule cursors are rebuilt
from the current definitions, with the next occurrence after import time.

Omit `workspace_id` to export the whole organization. Organization imports require
an instance without resources. `include_data` defaults to `true` for both scopes.
An omitted export request body uses those organization defaults.

## References and shared objects

Workspace entities receive fresh IDs. References are remapped in containment,
permissions, schemas, lookups, taxonomy values, view filters, smart folders and
workflow configuration, including UUID references embedded in expressions.
Ordinary item text, person subjects and exact numeric values retain their values.

Catalog content types are matched by key, which is unique in a catalog, and
their fields by field key; list types are copied with their collections and
inherit from the matched catalog types. Packages carry each type's parent, and
import rebuilds its lineage. Taxonomy groups are matched by name, sets by key, terms by set and
parent path, and directory groups by name. Existing shared definitions and group
memberships must agree; conflicts fail the entire import rather than changing
configuration another workspace uses. Content types and fields the target lacks are
copied. A group inbox already bound on the target conflicts.

User subjects are the portable directory identity. Existing target accounts keep
their active status, profile and organization roles. Missing referenced users are
created with the source profile/status and the ordinary Member role. Source
organization administration and custom role assignments do not transfer with a
workspace. The importing administrator retains administrative access.

A reference to another workspace, or a data reference omitted by a configuration
export, fails with an explanation. Make the workspace self-contained or use an
organization package. Personal Home and personal smart folders are excluded.
Workspace imports leave the target organization profile and preferences intact.

List integration credentials/settings are excluded: the result identifies which
integrations need configuration on the target. Imported integration items retain
their generic content and provider revision data but are detached from the source
connection. Credentials, audit records, live/delta cursors, tasks, staged files,
workflow runs and other execution state are installation-local.

Limits: 100000 records, 64 MiB manifest and 4 GiB ZIP/uncompressed file content.

Configuration-only workspace packages can declare `template_subject`, naming
exactly their one source account. On import, that account is bound to the importing
administrator in author metadata and user permissions. The target account's
profile, status and organization roles are retained; no template account is
created. These templates cannot contain groups, group inboxes or organization
identity settings. Use task selectors such as `role:owners` for portable reviewers;
prompts, field values and other literal text are not substituted. Ordinary
workspace copies preserve their source subjects. The [workflow samples](../samples/workflows/README.md)
and [receipt sample](../samples/receipts-package/README.md) are ready to import.

Packages require the current PaperGo contract; they are not a cross-product
PaperDotNet format. General parameter substitution and updating an existing
workspace are outside this workspace-copy operation.
