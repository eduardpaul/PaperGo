# Workspace portability review

PaperGo supports copying a complete shared workspace to another PaperGo instance,
with optional content. The target may already contain workspaces. PaperDotNet's
provisioning and portability documentation and implementation inform the feature;
PaperGo uses its own JSON manifest, reference remapping and transactional import.

| Requirement | PaperGo coverage |
|---|---|
| Export one workspace | `workspace_id` selects a shared workspace and its dependencies |
| Configuration only or with data | `include_data`; configuration keeps empty configured collections |
| Lists, libraries and settings | Containment, descriptions, versioning, publishing, WebDAV, duplicate policies and collection types |
| Content types and fields | Shared catalogs, validation rules, field options and frozen schemas |
| Templates and views | Workspace schema templates, default/saved list views and shared smart folders |
| Taxonomy and relationships | Referenced term sets and trees, relationship policies and optional content edges |
| Workflows | Definitions, parameters, versions and triggers; schedule cursors are rebuilt at import |
| Permissions and membership | Workspace members, unique ACLs and referenced groups with nested membership |
| Content and files | Typed values, folders, retained revisions/history, publications and all physical file versions |
| Import into a populated instance | New workspace and entity IDs, remapped internal references, optional new name |
| Preserve unrelated target configuration | Target organization/accounts/roles are retained; conflicting shared definitions fail atomically |
| Validation before application | Transactional dry run, ZIP/manifest bounds, checksums, graph ownership and reference checks |
| Browser and API access | Workspace settings entry point and administration controls; OpenAPI documents options and results |

The verification suite exercises API and UI request options, populated-instance
round trips with and without data, template type reuse, conflicting shared
configuration, accounts and roles, exact integers, literal text, reference
remapping, schedule rebasing, file versions, dry-run rollback, corrupted packages,
untrusted manifests and authorization. Organization transfer remains covered by
its configuration/content and integration round-trip tests.

## Deliberate boundaries

Imports create a workspace. They do not update or merge an existing workspace,
perform general parameter substitution or implement SharePoint/PaperDotNet
package compatibility. Configuration-only templates can explicitly bind their
sole author account to the importing administrator, with workspace-owner task
selectors. The workflow and receipt samples use this binding and ship as
directly importable ZIPs.
An existing live workspace name conflicts, preventing duplicate imports under the
same name. References to resources outside the selected workspace require a
self-contained workspace or an organization package. Organization imports require
an instance without resources.

Secrets, integration connection settings and execution state are not workspace
configuration. Integrations require configuration on the target. Personal Home
and personal smart folders are excluded from workspace packages. Shared taxonomy,
group or template definitions that conflict with the target must be resolved
before import; the importer does not overwrite objects used elsewhere.

See [the copy procedure and format rules](../workspace-portability.md).
