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

Membership bodies use `{"subject":"oidc-subject","role":"member"}`. The role defaults to `member`. Subjects are authenticated principal identifiers, must contain 1 to 255 UTF-8 bytes, and cannot have surrounding whitespace. PaperGo does not maintain a user directory or validate that the subject belongs to an active user.

| Role | Access |
| --- | --- |
| `owner` | Manage the workspace, memberships and all descendants, including exclusive child ACLs |
| `member` | Read drafts and contribute content through inherited access; child ACLs can restrict access |
| `visitor` | Read metadata and published content through inherited access; child ACLs can restrict access |

Membership changes return 204 and do not consume a workspace metadata ETag. Setting the current role is a no-op. Removing a missing member returns 404. Removing or demoting the last owner returns 409 with error code `last_owner`; assign another owner first. The write transaction and database constraints enforce this invariant. A caller without the required workspace access receives 404.

Workspace membership is stored as one root grant per subject. Root permissions are managed through the membership API; PUT `/v1/resources/{id}/permissions` applies to collections and content and rejects workspace roots. Breaking child inheritance can restrict members and visitors; workspace owners retain access. Explicit grants copied to a child scope remain independent of later root membership changes.

Deleting a workspace tombstones its entire live resource tree in one transaction, removes content search projections and live relationships, and disables and tombstones its workflows. New content and workflow access then fail. Immutable revisions, publications, workflow versions, run history, audit records and blob objects remain retained. Running workflow activities that access deleted content encounter the same missing-resource checks; deletion does not cancel durable runs.
