# Notifications

A notification tells a person that something happened. Where a [task](workflows.md#tasks) asks someone to act and waits for the answer, a notification needs no answer. Each notification is one message to one person, kept in their inbox until they delete it.

The model and its rules live in `internal/notify`, shared by tasks, workflow runs and the API. A notification is written in the transaction of the change it reports, so it exists exactly when that change committed. It starts no workflow and raises no domain event, so notifications cannot loop.

## What a notification holds

| Field | Meaning |
| --- | --- |
| `type` | `message` (sent by a person or a workflow), `task.assigned`, `task.completed` or `workflow.failed` |
| `title`, `body` | plain text of up to 300 and 4,000 characters, never rendered as HTML |
| `workspace_id` | the workspace it concerns |
| `item_id`, `task_id` or `run_id` | what it opens, at most one |
| `source` | `user`, `workflow` or `system` |
| `sender` | who sent it, or who caused what it reports |
| `workflow_id` | the workflow that sent it |
| `created_at`, `read_at` | when it arrived and when it was read |

## Rules

- **Access at send time.** A notification reaches only people who can read what it is about: its item, or else its workspace. A task's notification reaches only people who can review the task. Everyone else is skipped and counted. Each person's own access decides, not the limits of the sender's access token.
- **Nobody is told about their own action.** The person who caused a change is left out of the notifications about it. A message sent through the API reaches everyone it names, the sender included.
- **Once per dedup key.** A send with a `dedup_key` reaches only the people who do not have a notification with that key yet, so a repeated request sends nothing twice.
- **Bounded fan-out.** One send reaches at most 500 people once groups and roles are expanded; a larger send fails.
- **Inactive people** are left out.

## What PaperGo sends itself

| Type | Sent when | To |
| --- | --- | --- |
| `task.assigned` | a task is created, or reassigned | each person its assignees name, members of an assigned group included, who can review it; a reassignment tells only the people it adds. The person who assigned it is not told, unless a workflow created the task. |
| `task.completed` | a person's task is answered | its creator, unless they answered it. The body names who answered, the outcome and the comment. A workflow's task tells nobody: its workflow decides what follows. |
| `workflow.failed` | a run fails | the author of the run's workflow version, or the workspace's owners for a system built-in. Each workflow version tells each person at most once an hour, so a failing automatic workflow does not flood inboxes; the workflow's runs list every failure. Test runs tell nobody. |

Anything else a process wants to tell people, such as "your document was published", is a message a workflow sends, where people can see and change it.

## Sending messages

`POST /v1/workspaces/{id}/notifications` sends a message:

```json
{
  "recipients": ["role:owners", "group:legal", "field:reviewer"],
  "title": "Lease renewed",
  "body": "The lease now runs through 2031.",
  "item_id": "…",
  "dedup_key": "lease-renewed-2031"
}
```

- `recipients` takes the same entries as task assignees: people, `group:<id>`, `role:owners`, `role:members`, `creator`, `actor` (the caller) and `field:<key>`. Unknown people and groups fail with their names.
- The caller needs read access to the workspace and to `item_id`, an item of the workspace.
- The answer counts what happened: `{"sent": 2, "skipped": 1, "deduplicated": 0}`. `skipped` counts people who cannot read the item or workspace.
- A workflow's request sends the message from its workflow (`source: "workflow"`). The request is an exactly-once step, so a replayed run does not send it again:

```ts
await ctx.api.v1.workspaces.byId(ctx.run.workspace_id).notifications.post({
  recipients: ["creator"],
  title: `${item!.name} was not approved`,
  body: answer.comment,
  itemId: ctx.itemId,
});
```

The workspace's audit trail records each send as `notification.send`.

## Inbox

| Route | Does |
| --- | --- |
| `GET /v1/me/notifications?unread=&type=&workspace_id=` | the caller's notifications, newest first, paged with `$top` (up to 200, default 50) and `$skiptoken` |
| `GET /v1/me/notifications/unread-count` | `{"count": n}`, for a badge |
| `POST /v1/me/notifications/read` | `{"ids": [...]}` marks those read, `{"before": "<time>"}` every one created until then, and `{}` all of them; answers `{"marked": n}` |
| `DELETE /v1/me/notifications/{id}` | removes one |

People see and change only their own notifications; someone else's reads as not found.

Notifications are removed with what they link to: purging an item or a task removes its notifications, and a run that retention deletes leaves its notification without a link. Deleting a person removes their inbox.

## Live updates

The [live stream](application-foundation.md#live-api-updates) (`GET /v1/events`) sends a `notification` frame to the recipient's connections only, as a notification arrives, is read or is deleted:

```json
{"id": "…", "action": "created", "unread": 3, "notification": {"id": "…", "type": "task.assigned", "title": "Check the lease", …}}
```

A deleted notification's frame carries only its `id`, and only on a connection that delivered it.

## Web app

The bell in the top bar shows the unread count and the latest notifications; **Mark all read** clears the count. **View all notifications** (or `g` then `n`) opens the Notifications page, with All and Unread views, where each notification can be deleted. Opening a notification marks it read and goes to its task, its item in its list or library, or its run.
