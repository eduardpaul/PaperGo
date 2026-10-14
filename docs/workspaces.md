# Workspaces

A workspace contains lists, libraries and their content, plus workspace configuration and workflows. Its creator is an owner. Names are trimmed and require 1 to 200 UTF-16 code units; descriptions are optional and limited to 2000 UTF-16 code units.

| Method | Route | Behavior |
| --- | --- | --- |
| GET / POST | `/v1/workspaces` | List visible workspaces or create one with `name` and optional `description` |
| GET | `/v1/resources/{id}` | Read workspace metadata |
| PATCH | `/v1/resources/{id}` | Owner updates metadata with the workspace ETag |
| DELETE | `/v1/resources/{id}` | Owner deletes the workspace and its live content with the workspace ETag |
| GET | `/v1/workspaces/{id}/members` | Workspace readers list members, ordered by subject |
| POST | `/v1/workspaces/{id}/members` | Owner adds a subject or replaces its role |
| DELETE | `/v1/workspaces/{id}/members/{member}` | Owner removes a subject |

Membership bodies use `{"subject":"oidc-subject","role":"member"}`. The role defaults to `member`. Subjects must be active users in the organization directory, contain 1 to 255 UTF-8 bytes, and have no surrounding whitespace. Verified bearer or trusted-proxy subjects enroll on first authentication; organization identity managers can provision them through `/v1/identity/users`.

| Role | Access |
| --- | --- |
| `owner` | Manage the workspace, memberships and all descendants, including exclusive child ACLs |
| `member` | Read drafts and contribute content through inherited access; child ACLs can restrict access |
| `visitor` | Read metadata and published content through inherited access; child ACLs can restrict access |

Membership changes return 204 and do not consume a workspace metadata ETag. Additions, role changes and removals raise `permissions.changed` on the workspace root, with the member subject and previous/new role; the event commits with the membership and audit. See [workflow triggers](workflows.md#triggers). Setting the current role is a no-op. Removing a missing member returns 404. Removing or demoting the last owner returns 409 with error code `last_owner`; assign another owner first. The write transaction and database constraints enforce this invariant. A caller without the required workspace access receives 404.

Workspace membership is stored as one root user grant per subject. Root permissions are managed through the membership API; PUT `/v1/resources/{id}/permissions` applies to collections and content and rejects workspace roots. Breaking child inheritance can restrict members and visitors; workspace owners retain access subject to module scopes. Copying workspace inheritance stores dynamic visitor/member/owner role principals, so membership changes affect copied role grants immediately. Explicit user and group grants remain independent of parent ACL edits; group membership still resolves dynamically. The `workspace.manage` organization scope grants control of every live workspace and descendant, with the required module scopes.

Deleting a workspace tombstones its entire live resource tree in one transaction, removes content search projections and hides relationships, and disables and tombstones its workflows. New content and workflow access then fail. Immutable revisions, publications, workflow versions, run history, audit records and blob objects remain retained. Running workflow activities that access deleted content encounter the same missing-resource checks; deletion does not cancel durable runs.

Adding or changing a workspace membership rejects inactive users with `422`. Existing inactive memberships can be removed; reactivation is required before a role change.

## Personal Home

An active user discovers Home through GET `/v1/identity/me/home` with `list.read`; discovery needs no workspace creation or catalog administration permission. Home has a fixed sole owner. Its default Documents and Inbox libraries inherit that access. The organization administrator override and descendant ACLs apply. Ordinary deletion of Home or either default library, and changes to Home membership, return `protected_home` (409). Read-only `personal_owner_id` and `system_key` identify these resources.
