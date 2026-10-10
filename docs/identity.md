# Groups, roles and module scopes

PaperGo uses one organization per database. Set `ADMIN_SUBJECT` to the verified OIDC subject of its first administrator. Development authentication defaults this setting to `DEV_SUBJECT`. Startup creates the Administrator and Member roles once. Changing the setting after initialization does not grant another subject administrator access.

Verified bearer subjects enroll with the Member role on first use. Removing that assignment keeps it removed on subsequent authentication. Administrators can also enroll subjects through `POST /v1/identity/users` before adding them to groups or roles.

The directory lives under `/v1/identity`. `identity.read` permits listing users, groups, roles and the scope catalog; `identity.manage` permits changes. Group and role metadata changes and deletion require the current quoted integer `If-Match`. Membership and assignment PUT/DELETE operations are idempotent and address a `user` or `group` principal explicitly. Nested groups have a maximum depth of 10 and cannot contain cycles. Built-in roles are immutable. Changes cannot remove the last effective Administrator, including changes to nested group membership.

Role scopes are additive and resolve through direct assignments and nested group membership. `GET /v1/identity/me/scopes` shows the caller's current effective scopes. The Member role grants `workspace.read`, `workspace.create`, `list.read`, `list.write`, `contentType.read` and `taxonomy.read`. Organization administrators receive every catalog scope. Role and membership changes take effect without restarting the server or obtaining another token.

Content authorization combines module scopes and resource ACLs inside the database transaction. Workspace owners retain ACL control but still need the relevant module scope. `workspace.manage` is the explicit organization-wide ACL override; it does not grant other module scopes implicitly. Copying an ACL preserves group IDs, and copying workspace inheritance preserves dynamic workspace roles. REST, WebDAV and workflow item actions all use current persisted role and group membership.
