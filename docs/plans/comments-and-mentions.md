# Comments and mentions (plan)

Status: **proposed**. Nothing is implemented. This plan needs decisions on the core sockets in [extension points](#extension-points-this-needs) and on the [open decisions](#open-decisions) before phase 1.

The goal is a collaborative DMS. People can discuss a document or list item where it lives, pull a colleague in with an @mention, and turn a discussion into a task. Mentions, replies and resolved threads reach people through the same inbox as tasks.

## Product view

### Who it is for

| Person | Job | What they need |
| --- | --- | --- |
| Contributor | "Is this the signed version? The total looks wrong." | Ask a question on the item itself, not in email, and see the answer next to it. |
| Reviewer | "@legal please check clause 4 on page 3." | Point at a place in the file and bring in the right person. |
| Mentioned person | "Someone needs me." | One notification that opens the item at the thread, whatever raised it. |
| Owner or manager | "This must be done by Friday." | Turn a comment into a task with an assignee and a due date, and see that it was done. |
| Administrator | "Our public portal must not show internal discussion." "This tenant doesn't want comments." | Turn comments off, or limit who sees them, at any level. |
| Workflow author or assistant | "Explain why OCR rejected this file." "Summarise the discussion." | Read and post comments through the API, the SDK and MCP, under their own scope. |

### What "coherent" means here

1. **One inbox.** A mention is a notification, and an action request is a task. Comments add neither a new inbox nor new badges: they add notification types and a way to raise tasks.
2. **One place per item.** The item page has a **Discussion** tab. It shows threads together with the tasks raised from them, live, so the story of an item reads top to bottom.
3. **One permission model.** You see a comment exactly when you can see the thing it is on, under the collection's comment policy. A mention never grants access.
4. **One switch per level.** Each level has an on/off setting that inherits from the level above, and an administrator sees where the effective value comes from.

### Out of scope

- Real-time co-editing of documents.
- Chat channels that are not attached to an item.
- Reactions, rich text beyond links and mentions, and attachments in comments. These are phase 4 candidates.
- Turning an item into a to-do board. As the [tasks plan](tasks-and-notifications.md#standalone-tasks) says, that stays a list template.

## Where PaperGo stands

| Building block | State | What comments need from it |
| --- | --- | --- |
| Tasks (`internal/tasks`) | Implemented: create, respond, reassign, cancel, `task.*` events, Tasks screen | A Go call to create a task from a comment, and a check of who can see a task (today `visibleTo` is unexported) |
| Notifications | **Not built.** Phase 2 of the [tasks plan](tasks-and-notifications.md#phases): no `notifications` table, no `internal/notify`, no bell | Everything. Mentions are pointless without delivery, so this is the prerequisite |
| Domain events (`domain_events`, `dms.Raise`) | Generic: any `type` string | Nothing new to raise events |
| Workflow triggers (`internal/workflow/spec.go`) | Closed list: item, folder, file, permission, term, task (`tasks.Events` is named in the core), `wf.*` and role events | A way for a module to declare its trigger events (socket S3) |
| Live updates (`live_changes`) | `kind IN ('item','operation','run','access')` | A frame kind per module, authorized per recipient (socket S4) |
| Module scopes (`identity.Catalog`) | A fixed slice in `internal/identity` | A `comment.write` scope (socket S5) |
| Per-level settings | Ad hoc: columns on `resources` (`webdav_enabled`, `publishing_enabled`), workflow roles per collection, an organization/personal pattern in preferences | A generic, inherited per-resource setting a module owns (socket S2) |
| Feature discovery in the web app | `/auth/config` carries one ad hoc field, `mcp_url` | A list of the features that are on (socket S6) |
| Audit (`dms.Record`), authorization (`dms.Authorize`), writes (`dms.Write`), module routes (`httpapi.Module`) | Generic | Used as they are |
| Purge | `resources.Purge` deletes the resource rows | Module tables can reference `resources(id) ON DELETE CASCADE`, so no hook is needed |
| Workspace packages (portability) | `OnImport` hook only; export has no contributor | An export/import contributor so discussions move with a workspace (socket S7) |

## Extension-First assessment

**Can comments be an extension? Yes, almost entirely.** The module `internal/comments`, compiled in like `internal/documentsearch`, would own:
- its tables;
- its routes (`httpapi.Module`);
- its audit actions;
- its events;
- its web feature folder.

It acts through `dms.Authorize`, `dms.Write`, `dms.Raise`, `dms.Record` and `tasks.Create`.

It touches no base table: no `resources` columns, and no changes to `tasks` or `item_revisions`.

What it cannot do alone is plug into the four other places a feature must show up:
- the trigger catalog;
- the live stream;
- the scope catalog;
- the settings and notification registries.

Today each of these is a closed list in core. These are the sockets below. Each is generic, and each lets the core stop hard-coding something it already hard-codes, such as `tasks.Events` in the workflow spec and `mcp_url` in `/auth/config`.

### Rejected designs

- **Comments as list items in a hidden collection.** This would give search and permissions for free. But every comment would become a revision, a surface, field indexes and an `item.updated` event, which is heavy write amplification. Visibility would follow the hidden list rather than the commented item. Mentions and threads would have no relational shape.
- **Comments in `activity_records`.** Those records are generic JSON with leases and expiry, built for execution state. They give no indexed queries for "threads on this item" or "mentions of me", and nothing cascades on purge.
- **Comments inside `internal/tasks` or `dms`.** This is core bloat. Deployments that turn comments off would still carry them.

## Extension points this needs

AGENTS.md requires asking before building any of these. Each one is listed with what else it serves.

| # | Socket | Shape | Other users |
| --- | --- | --- | --- |
| S1 | **Notification type registry** (part of notifications phase 2) | `notify.RegisterType(Type{Key, Label, Description, Mandatory, DefaultChannels})`. Settings list the registered types, and a mandatory type always reaches the inbox | Tasks (`task.assigned`…), workflows (`workflow.failed`), search ("reindex finished"), future follows |
| S2 | **Inherited resource settings** | `dms.RegisterSetting(Setting{Namespace, Schema, Default, Levels})`, a namespaced JSON value per organization, workspace, collection, folder or item, stored in one table `resource_settings(resource_id, namespace, value, version)` with an organization row. `dms.EffectiveSetting(ctx, ns, resourceID)` returns the value and the level it came from. Writes take `If-Match`, are audited, and raise `settings.changed` | WebDAV, publishing defaults, AI features, the search opt-out (today a role toggle), any later module. Existing columns stay as they are. Moving them is a separate decision |
| S3 | **Module trigger events** | `workflow.RegisterEvents(family string, events []string, filters FilterSpec)`. Replaces the hard-coded `tasks.Events` case in `spec.go`, with tasks as its first user | Tasks, comments, future follows and integrations |
| S4 | **Module live frames** | `live.RegisterKind(kind, func(ctx, subject, entityID) (frame any, visible bool))`. This drops the `CHECK (kind IN (...))` in favour of registered kinds, validated in Go | Tasks and notifications (already planned), comments |
| S5 | **Module scopes** | `identity.RegisterScope(name, grantedToMembers)` before startup, so that `Catalog` becomes the core scopes plus registered ones | Comments (`comment.write`), any module with its own write permission |
| S6 | **Feature discovery** | `/auth/config` returns `features: {"comments": {...}, "search": {...}, "mcp": {"url": "..."}}`, filled by registered modules. `mcp_url` becomes `features.mcp.url` | The web app hides the UI of features that are off. Search and MCP move onto it |
| S7 | **Package contributors** | `dms.AddPackageContributor{Export, Import}` beside `OnImport`, so a module's rows travel with workspace export and import | Comments, tasks (not exported today), saved module settings |

Phase 1 needs S1, S2, S4, S5 and S6. S3 and S7 are needed in phase 2.

## Model

### Tables (module-owned, in both DAL schema masters)

`comments`:

| Column | Meaning |
| --- | --- |
| `id`, `created_at`, `updated_at`, `version` | identity and ETag |
| `item_id` / `task_id` | the target: exactly one is set. `REFERENCES resources(id) ON DELETE CASCADE` / `tasks(id) ON DELETE CASCADE`, so purging an item removes its discussion. There is no `workspace_id`: it comes from the target, so a cross-workspace transfer needs no rewrite |
| `thread_id` | the root comment's ID; a root has `thread_id = id`. Threads are one level deep (a root and its replies), as in GitHub and Google Docs |
| `author` | subject |
| `source` | `user` or `workflow` (with `run_id`), shown as "via workflow X" |
| `body` | plain text of up to 10,000 characters, with mention tokens `<@user:alice>` and `<@group:id>`. Rendered as text, links autodetected, never HTML. Names are resolved when the comment is read, so a renamed user shows their current name |
| `anchor` (phase 3) | `{revision_id, blob_id, page, rect, quote}`: a place in a file version. It is shown as outdated once the item's file changes |
| `resolved_at`, `resolved_by` | on roots only |
| `task_id_raised` | the task raised from this comment (see [tasks](#tasks)) |
| `edited_at`, `deleted_at`, `deleted_by` | an edit keeps the previous body in the audit details. A deleted comment leaves a "deleted" placeholder while it has replies |

`comment_mentions(comment_id, principal_type, principal_id, notified)` supports "mentions of me" and the diff on edit: only new mentions notify.

`comment_reads(thread_id, subject, read_at)` (phase 2) supports the unread marks per thread and "new since you last looked".

Indexes: `(item_id, thread_id, created_at)`, `(task_id, …)`, `comment_mentions(principal_id, comment_id)`.

### Settings: on and off at every level

There is one setting namespace, `comments`, held by S2:

| Level | Who changes it | Value | Inherits from |
| --- | --- | --- | --- |
| **Deployment** | operator: `COMMENTS=on\|off` (default `on`) | Off: no routes, tables unused, no UI, no notification types, no triggers, no scope | — |
| **Organization** | `organization.manage` | `enabled`, `mentions: workspace\|directory` (who can be suggested), `group_mentions: bool`, `max_group_fanout` | built-in defaults |
| **Workspace** | workspace managers | `enabled` | organization |
| **Collection** (list, library) | `list.manage` | `mode: off\|collaborators\|readers` | workspace |
| **Folder, item** | `list.manage` (folder), managers (item) | `locked: bool`: the discussion stays readable, but nobody can post | collection |
| **Person** | the user | notification channels per comment type, muting a thread, unfollowing | organization notification defaults |
| **Credential** | token creator | `comment.write` scope; reading follows `item.read` | — |

`mode`:
- `collaborators` (the default) means people who can read the draft (`read_draft`) can read and post. A published-only portal visitor sees no internal discussion.
- `readers` means anyone who can read the item can read and post, which suits a knowledge base with Q&A.
- `off` hides existing threads but keeps them, so turning comments back on brings them back.

The API returns the effective value with its origin (`"from": "workspace"`), and every settings screen shows "inherited from …".

### Permissions

| Action | Who |
| --- | --- |
| Read a thread | can read the target under the collection's `mode`; for a task, can see the task (assignees, creator, workspace owners) |
| Post or reply | read, plus not locked, plus the `comment.write` scope; the item is not archived or deleted |
| Edit | the author |
| Delete | the author, or `manage` on the item |
| Resolve or reopen | the thread's author, people with `write` on the item, and managers |
| Lock | `manage` |

Every check runs through `dms.Authorize`, so token scopes and the nearest exclusive permission scope apply, as everywhere else.

### Mentions

- **Suggestions:** `GET /v1/items/{id}/mentionable?q=` lists people (and groups, if allowed) matching `q`. Each carries `can_see: bool` under the comment mode, so the composer can warn "Carol can't see this item". A mention never shares the item. Requesting access is a separate feature.
- **Delivery:** at send time, each mentioned principal is expanded (groups through `GroupClosures`), people who can't see the thread are filtered out, and so is the author. The fan-out is capped as in the notifications plan (500). Above the organization's `max_group_fanout`, a group mention is refused with a clear message.
- **Dedup key:** `comment:{id}:mention:{subject}`. An edit that adds a mention notifies only the new person.

## Integration with notifications

These are the types comments register through S1:

| Type | Sent to | Mandatory in the inbox | Default external channels |
| --- | --- | --- | --- |
| `comment.mentioned` | the people mentioned | yes, like `task.assigned` | email |
| `comment.replied` | the thread's participants (author and repliers), except the actor | no | none |
| `comment.resolved` | the thread's author | no | none |
| `comment.activity` (phase 3, with follows) | the item's followers | no | daily digest |

Rules from the [notifications plan](tasks-and-notifications.md#rules) apply unchanged: access at send time, no notification about your own action, bounded fan-out, and dedup.

Comments add one rule: **coalescing**. A burst of replies in a thread updates one unread notification per recipient ("3 new replies on *Invoice 1042*"), keyed by `comment:thread:{id}:{recipient}`, instead of stacking up. This can be a general `notify.Message.Coalesce` key, useful for task reminders too.

The link opens the item's Discussion tab at the thread (`{item_id, thread_id}`). A notification raises no domain event, so comment notifications cannot loop.

## Integration with tasks

1. **Raise a task from a comment.** A comment's **Make a task** action calls `POST /v1/comments/{id}/task`. It is prefilled with:
   - the mentioned people as assignees;
   - the first line of the comment as the title;
   - the comment as the description;
   - the item.

   The module calls `tasks.Create` in the same transaction, records the task as `task_id_raised`, and the core sends `task.assigned`. People still need `item.write`-level access to create a task on an item, as for standalone tasks today.
2. **Show the task in the thread.** The thread renders the raised task as a card with its live status, assignees, due date and, once done, its outcome and comment. This is read from the task, not copied, so nothing reacts to changes in hidden code.
3. **Optionally resolve the thread when the task completes.** This is the built-in workflow `comments.resolve_on_task`, off by default. It triggers on `task.completed` and resolves the thread whose comment raised the task. People can see it, turn it on per collection, or change it, which is the PaperGo rule for reactions.
4. **Discussion on a task.** Comments target tasks too (`task_id`). This answers "which total do you mean?" without abusing the single response comment. Visibility is the task's visibility.
5. **Approvals.** A rejection's required comment stays on the task's response. The item's Discussion tab shows tasks about the item (from `GET /v1/items/{id}/tasks`) in the same timeline, so the rejection reason is visible where people talk.

## Integration with workflows, the API, the SDK and MCP

- **Events** (S3): `comment.created`, `comment.resolved` and `comment.deleted`, with `comment_id`, `thread_id`, `item_id`/`task_id`, `mentions` and `source`. Trigger filters are `collection_id` and `mentions_any` (people or groups). A workflow's own comment raises its event one level deeper, so loop protection holds.
- **Script API:** `ctx.api.v1.items.byId(id).comments.post({body})` posts as the workflow version's author, with `source: workflow`. A use case is a bot that explains why a document failed validation and mentions the uploader.
- **REST routes:**
  - `GET /v1/items/{id}/comments?status=open|resolved|all`
  - `POST /v1/items/{id}/comments`
  - `GET /v1/tasks/{id}/comments`, `POST /v1/tasks/{id}/comments`
  - `GET`, `PATCH` (`If-Match`) and `DELETE /v1/comments/{id}`
  - `POST /v1/comments/{id}/resolve` and `/reopen`
  - `POST /v1/comments/{id}/task`
  - `GET /v1/me/mentions`
  - `POST /v1/comments/counts` with `{item_ids}`, for list columns, batched and authorized
  - `GET`/`PATCH /v1/resources/{id}/settings/comments` (S2)

  All of these are in `api/openapi.json`, with the SDK regenerated.
- **MCP** (`extensions/mcp`):
  - `list_comments(item_id)`, with `item.read`;
  - `add_comment(item_id, body, reply_to?)`, with `comment.write`.

  This gives a read-only assistant a safe way to contribute ("leave review notes") without `item.write`. Both tools are hidden when the deployment reports comments off (S6).
- **Search** (phase 4): comment text in the item's search document, through the search extension. It is optional, and visibility of comments follows the mode at query time.

## Web app

- **Item page, Discussion tab:**
  - a count badge;
  - open and resolved filters;
  - threads with replies, the composer with @-autocomplete and the "can't see" warning;
  - **Make a task**, plus resolve, edit and delete;
  - the item's tasks inline in the timeline.
- **Document viewer** (phase 3): select a region or text on a page to comment, with pins on pages and "outdated" on older file versions.
- **Lists:**
  - an optional comment column (count and unread dot) fed by the batched counts route;
  - a filter "has open discussion" (phase 3, needs an index the module owns).
- **Inbox:**
  - the Notifications tab gains a **Mentions** filter;
  - the bell counts mentions like any notification;
  - a task's detail shows its discussion.
- **Settings:**
  - Organization → Collaboration;
  - Workspace → Comments;
  - Collection → Comments (mode);
  - Item → Lock discussion;
  - Personal → Notifications (per-type rows registered by S1).
- **Feature-gated:** with comments off at the deployment or organization level, none of this renders. With comments off for a collection, the tab says "Comments are off for this list" to managers and is hidden for others.

The code lives in `web/src/features/comments`, and the item page renders feature tabs from a small registry, so the module plugs in rather than being wired into the page.

## Lifecycle and compliance

| Event | Comments |
| --- | --- |
| Item deleted (recycle bin) | hidden with the item, and restored with it |
| Item purged, or a workspace or collection purged | removed through the foreign key cascade |
| Item archived | readable, no new posts (proposed; can become a setting) |
| Item moved or transferred | follow the item; visibility is evaluated against its new place |
| Task cancelled or completed | discussion stays readable; posting stays open (to discuss the outcome) |
| Comment edited or deleted | audit records `comment.create`, `comment.edit` (previous body), `comment.delete` and `comment.resolve` with the credential, as the MCP work added |
| Workspace export and import (S7) | threads, mentions and the comment settings travel with the workspace |
| Comments turned off | data is kept; turning them on restores everything |

## Phases

Each phase ships with its tests (SQLite and PostgreSQL), OpenAPI, SDK, docs and web screens.

0. **Prerequisites.**
   - Notifications core: phase 2 of the [tasks plan](tasks-and-notifications.md#phases), with the type registry (S1) and coalescing.
   - Sockets S2, S4, S5 and S6, each with its first existing user moved onto it: tasks and notifications frames on S4, `mcp_url` on S6.
1. **Item discussions (MVP).**
   - The module, its tables and routes.
   - Threads, replies, edit and delete, resolve.
   - User mentions with access checks, `comment.mentioned` and `comment.replied`.
   - Settings at deployment, organization, workspace and collection level.
   - Live frames, audit, the `comment.write` scope.
   - The Discussion tab, the inbox Mentions filter, the counts column.
2. **Tasks and automation.**
   - Comments on tasks, **Make a task**, task cards in threads, the `comments.resolve_on_task` built-in.
   - Trigger events (S3) and script examples.
   - MCP tools.
   - Package export and import (S7).
   - Unread marks per thread.
3. **Documents and reach.**
   - Anchored comments on previews and file versions, with outdated anchors.
   - Group mentions with fan-out limits.
   - Folder and item locks.
   - Follows and `comment.activity` with digests, after notifications phase 4 (email).
4. **Later.** Reactions, comment search, attachments in comments, requesting access from a mention.

## Measuring it

- Share of active items with at least one thread, per collection.
- Time from mention to the mentioned person's first reply or visit.
- Tasks raised from comments, and their completion rate.
- Notification read rate per comment type, and how many people mute a type. A high mute rate means the defaults are too noisy.
- Where comments are turned off, and at which level. Many collections off can point to a missing `mode`.

## Open decisions

1. **Build the sockets S1–S7?** They are core changes, so AGENTS.md requires approval. Each is generic and replaces something the core already hard-codes.
2. **Notifications first.** Comments are not worth shipping without delivery, so phase 2 of the tasks plan must come first. Is that the right order?
3. **Default visibility.** Should the default `mode` be `collaborators` (draft readers; recommended for a DMS with published portals) or `readers`?
4. **Whom mentions suggest.** Should it be the workspace's members (recommended) or the whole directory, with the "can't see" warning?
5. **Scope.** Should there be a separate `comment.write` (recommended: lets read-only assistants and integrations comment) or should posting fall under `item.write`?
6. **Archived items.** Should they be read-only for comments (proposed) or open?
