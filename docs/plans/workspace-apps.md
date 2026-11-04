# Workspace apps (plan)

Status: proposal. Nothing here is implemented. Phase 1 needs the core changes listed under [Core changes](#core-changes), which need approval first (Extension-First Protocol, [AGENTS.md](../../AGENTS.md)). The first app is the [diesel tracker](use-case-diesel-analysis.md).

## What an app is

An **app** is a React screen installed into a workspace. Members open it from the sidebar.

- **It does full CRUD by itself.** It creates, reads, queries, updates and deletes items of the workspace's lists through the PaperGo REST API, as the person using it. No workflow is needed for that.
- **It can bring workflows.** An app may install workflows it owns, start them and read their results, and find the tasks they create. Workflows do what the viewer may not, or what must happen whoever writes the data.
- **It is its own feature.** Apps are installed from an **app package**, not through workspace export and import. A workspace export only says which apps the workspace needs.

```
 Browser                                   PaperGo server
 ───────                                   ─────────────────────────────────────
 PaperGo page ── sandboxed frame: app  ──►  REST API (as the viewer, limited to
                 React + @papergo/app       the app's workspace) ── lists & items
                                            │
                                            └─ app-owned workflows (as the installer)
                                                 └─ their tasks
```

## The app package

A ZIP with one manifest, one screen and the app's workflows (the manifest below is shortened):

```
diesel.zip
  app.json
  app.tsx
  workflows/diesel-levels.ts
  workflows/diesel-measure-reminder.ts
  workflows/diesel-refill.ts
```

```json
{
  "key": "diesel",
  "name": "Diesel tracker",
  "version": "1.0.0",
  "setup": {
    "fields": [{ "key": "depth_cm", "label": "Depth (cm)", "type": "decimal", "scale": 1, "required": true }],
    "content_types": [{ "key": "diesel_reading", "name": "Reading", "fields": ["measured_at", "depth_cm", "level_liters"] }],
    "lists": [{ "key": "diesel", "name": "Diesel", "content_types": ["diesel_reading", "diesel_purchase"] }]
  },
  "parameters": { "list": "$list.diesel" },
  "workflows": [
    { "key": "diesel-levels", "script": "workflows/diesel-levels.ts", "parameters": { "list": "$list.diesel", "capacity_l": 200 } }
  ]
}
```

- **`setup`** declares the fields, content types and lists the app needs. Installing creates them in the workspace, or reuses a field or type whose definition already matches by key. `$list.<key>` and `$type.<key>` in parameters become the IDs installation creates.
- **`parameters`** are the app's own settings, which `app.tsx` reads as `ctx.params`. The owner can change them after installing.
- **`workflows`** are ordinary workflow scripts with their parameters. Installing creates them as workflows **owned by the app**, authored by the installer.

## Installing and removing

**Install.** In **Workspace settings → Apps → Install**, a workspace owner uploads the ZIP. PaperGo checks the manifest, compiles `app.tsx` and every workflow, and lists what it will create. Then it creates the setup, the workflows and the app in one transaction.

**Uninstall.** **Remove** deletes the app and the workflows it owns. Deleting a workflow already cancels its runs and open tasks, and removes their notifications. **Answered tasks are kept.** The lists and their items are workspace data and stay. The owner can choose **also delete its lists** to remove them too, which moves them to the recycle bin like any deleted list.

**Workspace export and import.** A workspace package carries the lists and their data as today. It leaves out apps and the workflows they own, and lists the apps the workspace needs instead:

```json
"apps": [{ "key": "diesel", "name": "Diesel tracker", "version": "1.0.0" }]
```

The import's dry run and result say "This workspace uses Diesel tracker 1.0.0. Install it from its app package."

## The screen

```tsx
import { app, json, useQuery } from "@papergo/app";

export default app<{ list: string }>({
  render: (ctx) => {
    const rows = useQuery(ctx.params.list, { sort: [{ field: "measured_at", direction: "desc" }] }, { live: true });
    const add = (depth: string) =>
      ctx.api.v1.resources.byId(ctx.params.list).children.post({ kind: "item", name: "Reading", values: json({ depth_cm: depth }) });
    return <Dashboard rows={rows.data} onAdd={add} />;
  },
});
```

| `ctx` | What it is |
| --- | --- |
| `ctx.api` | The generated REST client (the workflow SDK's), sent as the viewer. Full item CRUD on the workspace's lists. |
| `ctx.params` | The app's parameters. |
| `ctx.user`, `ctx.workspace` | Who is viewing, and where. |
| `ctx.run(key, input, { itemId })` | Starts one of the app's workflows and resolves with its output. |
| `ctx.workflows()` | The app's workflows: key, name, manual input form. |
| `ctx.tasks({ status })` | The tasks of the app's workflows, and those created through the app, that the viewer may see. |

`useQuery` and `useItem` re-read when the list changes, through the page's live stream.

## How it runs

1. The PaperGo page reads the app's compiled code with the viewer's login. It starts a sandboxed frame (`sandbox="allow-scripts allow-forms"`, no network) and hands the code in.
2. The app's requests travel to the page by `postMessage`. The page sends each one as the viewer, with `PaperGo-App: <app id>`, to the routes apps may call: items, folders, queries, views, content types, comments, tasks, and the app's own workflows and runs.
3. With that header, the server limits the request to the app's workspace (the place limit [limited access tokens](../identity.md) already apply), and applies the viewer's own permissions.

An app never does more than its viewer may, and only in its workspace. Its frame never sees a login, so an app author cannot act through an administrator who opens it.

**Workflows and tasks of an app.**
- Each workflow keeps an `app_id`. A task keeps an `app_id` when an app-owned workflow's run or a request through the app creates it.
- `ctx.workflows()` and `ctx.tasks()` filter by it.
- Anyone who may open an app may start its workflows' manual runs. The runs act as the workflow's author, as every run does.

## Phase 1: make it work

The diesel tracker, end to end, with nothing more than it needs.

| Piece | Change |
| --- | --- |
| Storage | An `apps` table: workspace, key, name, version, manifest, source, bundle, parameters, installer. `app_id` on `workflows` and `tasks`. Deleting a workspace deletes its apps. |
| Install and remove | `POST /v1/workspaces/{id}/apps` (the ZIP), `GET /v1/workspaces/{id}/apps`, `GET`, `PATCH` (parameters) and `DELETE /v1/apps/{id}` (`delete_lists=true` optional). |
| Compile | `app.tsx` with the esbuild the server already embeds (TSX, imports `react` and `@papergo/app` only); workflows with the existing workflow compiler. |
| Requests | The `PaperGo-App` header limits a request to the app's workspace and stamps `app_id` on tasks it creates. `app_id` filters on workflow and task lists. App-owned manual workflows can be started by anyone who can open the app. |
| Export | Workspace packages leave out apps and app-owned workflows, and list `apps` in the manifest. Import reports them. |
| SDK | `@papergo/app`: the generated client over a `postMessage` host, `app()`, `useQuery`, `useItem`, `ctx.run`, `ctx.workflows`, `ctx.tasks`. React ships in the runtime bundle. |
| Web | Apps in the sidebar; the app page with its sandboxed frame and bridge; **Workspace settings → Apps** with Install and Remove. |
| Sample | `samples/diesel-app/diesel.zip`, built from its sources. |

Not in phase 1: an in-browser editor (apps are written as files and installed as a ZIP), version history, a UI kit, upgrades, per-list access.

## Phase 2: make it right

In order of need:

1. **Upgrade in place.** Install a newer version of the same app key. The app and its workflows are replaced by key, missing setup is added, and nothing is deleted.
2. **Install into an imported workspace.** Installing an app the import listed reuses its imported lists instead of creating new ones.
3. **Per-list access.** The manifest declares the lists the app reads or writes, and the server limits requests to them.
4. **Editor.** Apps in the workflow editor's Monaco, with type checking, a live preview, versions and drafts.
5. **UI kit.** Forms from content types, stat tiles, tables and charts in PaperGo's theme.
6. **Package identity.** App workflows run as an identity that reaches only their workspace.
7. **About.** Publisher, readme and required features (AI, OCR, search), checked on install.

## Core changes

These need approval before phase 1. Each is generic beyond apps.

| Change | Also useful for |
| --- | --- |
| `app_id` on workflows and tasks, with list filters | Any feature that owns workflows, such as integrations |
| A request header that limits a request to one workspace and names its source in tasks | Embedded views, scoped MCP connections |
| Workspace packages leave out owned workflows and list required apps | Any installable extension |
| Members may start the manual runs of an app's workflows | Buttons in other clients |
| A sandboxed frame host and `postMessage` bridge in the web app | Custom views and item panels |

The `apps` table, its routes and the runtime belong to the apps feature itself.

## Out of scope

A store, licences or payments: a package is a file. Network access from apps. Public or anonymous apps. Native mobile apps.
