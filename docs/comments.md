# Comments

People discuss an item where it lives, in threads of comments, and bring others in by mentioning them. Mentions and replies reach people as [notifications](notifications.md). The model and its rules live in `internal/comments`; the comments, the people they mention and the comment settings are tables of the DAL schema.

## Threads

- A **thread** is a first comment and its replies, one level deep. Starting a comment without `thread_id` starts a thread; with it, the comment answers that thread.
- A comment is plain text of up to 10,000 characters, never rendered as HTML. A thread has at most 500 replies.
- **Mentions** are written `<@user:subject>`, at most 50 per comment. A mention a comment adds must name an active person; someone who leaves stays mentioned where they were, so the comment can still be edited. The API shows each comment's mentions and author with their current names.
- **Editing** replaces a comment's text; only its author edits. The comment shows that it was edited, its notifications show the new text, and the audit trail keeps the previous text.
- **Deleting** empties a comment and keeps its place: its author, or the item's managers, delete it. Its text leaves people's inboxes too: a notification of the comment alone is deleted, and a folded reply notification counts one fewer, without the text, and opens the thread. A thread whose first comment is deleted stays while it has replies, and nobody can answer it any more.
- **Resolving** marks a thread done, and reopening opens it again: the thread's author, people who may change the item and its managers may. Threads list as open (the default), resolved or all.
- Each change carries a version: edits, deletions and resolving need the comment's ETag in `If-Match`.
- A comment written by a workflow run shows the workflow (`source: "workflow"`).
- Purging an item removes its discussion. Deleting an item to the recycle bin hides it with the item, and restoring brings it back.

## Who takes part

A discussion follows its item. Each list or library has a **mode**:

| Mode | Who reads and writes |
| --- | --- |
| `collaborators` (default) | people who can read the item's draft (`read_draft`) |
| `readers` | everyone who can read a revision of the item, published or draft |
| `off` | nobody; the comments are kept and come back when comments are on again |

The mode is inherited, and each level shows where the mode that applies comes from (`mode_from`, `effective.from`):

| Level | Set by | Setting |
| --- | --- | --- |
| Deployment | the operator: `COMMENTS=on` (default) or `off`, or `comments` in `config.yaml` | off removes the comment routes, and the web app shows no discussions |
| Organization | `organization.manage`: `PUT /v1/organization/settings/comments` | `enabled`, a switch that turns comments off everywhere whatever the levels below set, and the default `mode` |
| Workspace | its managers: `PUT /v1/resources/{id}/settings/comments` | `mode`, or `null` to inherit the organization's |
| List or library | its managers, the same route | `mode`, or `null` to inherit the workspace's |

A setting that was never written has version 0: its first `PUT` sends `If-Match: "0"`.

- **Writing** needs read access to the discussion and the `comment.write` scope, which the Member role grants. A read-only access token reads discussions and writes nothing.
- An **archived item's** discussion is read only.
- A **mention never shares the item.** People who cannot read the discussion are not told, and the web app warns when someone picked cannot see the item.

## Notifications

| Type | Sent to | Notes |
| --- | --- | --- |
| `comment.mentioned` | the people a comment mentions, and those an edit adds | once per comment and person |
| `comment.replied` | the thread's other participants: its first comment's author and those who replied | a burst of replies folds into one unread notification per person, whose `count` says how many it stands for and whose text, sender and source are the latest reply's; once read, the next reply starts a new one |

Only people who may read the discussion are told, never the comment's author. A mentioned person is told of the mention, not also of the reply. The title names the item as everyone told can read it: its draft's name among collaborators; among readers its published name, or its draft's when it was never published. Each notification opens the item's thread (`item_id`, `comment_id`). An integration's token mentions and replies like a person, with the access its token gives it.

## API

| Route | Does |
| --- | --- |
| `GET /v1/items/{id}/comments?status=open\|resolved\|all` | the item's threads, oldest first, each with its replies, paged with `$top` (up to 100) and `$skiptoken`; with `mode`, `mode_from`, `can_read` and `can_post`. Where comments are off or the caller may not read the discussion, it lists none and says why |
| `POST /v1/items/{id}/comments` | `{body, thread_id?}`: starts or answers a thread |
| `GET`, `PATCH` (`{body}`), `DELETE /v1/comments/{id}` | reads, edits or deletes a comment |
| `POST /v1/comments/{id}/resolve`, `/reopen` | resolves or reopens a thread |
| `POST /v1/comments/counts` | `{item_ids}`, up to 200: open and resolved thread counts of the items whose discussions the caller may read |
| `GET /v1/items/{id}/mentionable?q=` | up to 20 of the people the item is shared with (granted on it, its folders, its list or library, or its workspace, in person or through a group) whose subject, name or email contains `q`, each with `can_see` |
| `GET`, `PUT /v1/resources/{id}/settings/comments` | a workspace's, list's or library's mode |
| `GET`, `PUT /v1/organization/settings/comments` | the organization's switch and default mode; `available` is false when the deployment runs without comments |

Changes are recorded in the workspace's audit trail: `comment.create`, `comment.edit`, `comment.delete`, `comment.resolve`, `comment.reopen` and `settings.comments`. The [live stream](application-foundation.md#live-api-updates) sends a `comment` frame, naming the comment, its item and thread, to the connections of people who may read the discussion.

Workflow scripts comment through the same route: `ctx.api.v1.items.byId(id).comments.post({ body })`.

## Web app

An item's panel has a **Discussion** tab with the open, resolved and all threads, their replies, and a box to start a thread; **Load more threads** continues past the first 50. Typing `@` suggests the people the item is shared with; a person picked appears as `@Name`, or `@Name (subject)` when someone else mentioned shares the name, so editing a comment never swaps one person for another. Opening a mention or reply notification shows every thread, loads pages until the comment's thread is there, and brings it into view, resolved or not. Each comment offers edit and delete to those who may, and each thread offers reply, resolve and reopen. The tab says when comments are off and which level turned them off, or that the discussion is for collaborators.

Lists, libraries and workspaces choose their mode on their settings page; the organization turns comments on or off and chooses the default mode under Administration. The Notifications page has a **Mentions** view, and a folded reply shows how many it stands for.
