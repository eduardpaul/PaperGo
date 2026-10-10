# Workspace alignment — code/API implementation

Reference: PaperDotNet commit `96df412f040f53cc14711aaba35880e24817e084`. PaperGo evidence below is the current working tree. This report concerns shared workspace stories and uses executable source and tests as evidence.

| User story | Result | PaperGo implementation / remaining difference |
| --- | --- | --- |
| Create and discover a workspace | Implemented | Creator becomes owner; visible workspace listing uses membership permissions. Name is trimmed and limited to 200 UTF-16 code units. |
| Describe and rename a workspace | Implemented | Optional description with a 2000 UTF-16 limit; owner-only updates with metadata ETags. [Service](C:/Users/eduapaul/Documents/PaperGo/internal/dms/service.go) and [validation](C:/Users/eduapaul/Documents/PaperGo/internal/dms/workspaces.go). |
| List members and assign roles | Implemented, Identity dependency remains | GET / POST `/v1/workspaces/{id}/members`; roles `owner`, `member`, `visitor`; default member; ordered results; role replacement and idempotent retries. PaperGo accepts external authenticated subject IDs. PaperDotNet validates active local user GUIDs. [Membership service](C:/Users/eduapaul/Documents/PaperGo/internal/dms/workspaces.go). |
| Remove a member or transfer ownership | Implemented | DELETE member; missing member 404; last-owner removal/demotion 409; independent of metadata ETags. Database also prevents duplicate membership roles and moving the owner grant. [Schema](C:/Users/eduapaul/Documents/PaperGo/migrations/20261007000100_schema.sql). |
| Owners retain control of workspace content | Implemented | Owners can manage descendants through exclusive child ACLs. Members and visitors remain constrained by child ACLs. [Authorization](C:/Users/eduapaul/Documents/PaperGo/internal/dms/permissions.go). |
| Delete a workspace and withdraw its content | Implemented | Owner and ETag required; one transaction tombstones the live tree, clears search projections and relationships, and disables/tombstones workflows. Retained immutable history and durable runs follow PaperGo's storage policy. [Deletion](C:/Users/eduapaul/Documents/PaperGo/internal/dms/service.go). |
| Provision and protect a personal home workspace | Still missing | PaperGo has no personal-workspace marker or Identity home provisioning. PaperDotNet rejects personal workspace deletion and membership additions. This shared-workspace implementation does not cover that Identity integration. |
| Organization administrators manage all workspaces | Still missing | PaperGo has external subject authentication and resource authorization, without PaperDotNet's organization role directory and workspace OAuth scope checks. |

API shape differences remain explicit: PaperGo uses `/v1`, snake_case and external subjects; metadata GET/PATCH/DELETE use `/v1/resources/{id}`. Stale metadata ETags return PaperGo's existing 409 rather than PaperDotNet's 412. PaperGo supplies membership roles through the membership list rather than PaperDotNet's per-workspace `access` / `isPersonal` response fields. These are not compatibility routes or duplicate membership stores.

Memberships have one canonical root ACL grant per subject: manage for owner, write for member and read for visitor. The generic ACL setter rejects workspace roots. Copied child grants remain independent of root membership changes.

Validation evidence: [DMS workspace tests](C:/Users/eduapaul/Documents/PaperGo/internal/dms/workspaces_test.go), [HTTP workspace tests](C:/Users/eduapaul/Documents/PaperGo/internal/httpapi/workspaces_test.go) and [workflow permission-retry tests](C:/Users/eduapaul/Documents/PaperGo/internal/runner/runner_test.go). The single schema is edited in place, Ent is regenerated, and the Atlas checksum is refreshed. The original parity report records its pinned baseline; this report describes the workspace implementation added afterward.
