# Workspace apps (architecture plan)

Status: proposal. Nothing here is implemented. The core sockets in [Extension-first assessment](#extension-first-assessment) need approval before any code is written (see the Extension-First Protocol in [AGENTS.md](../../AGENTS.md)). The first app is the [diesel tracker](use-case-diesel-analysis.md).

## Goal

A **workspace app** is a screen that a workspace owner writes in PaperGo, in TypeScript and React, and that the workspace's members open from the sidebar. Writing one should feel like writing a [workflow](../workflows.md): one file, one SDK, the same editor, versions, parameters and packages. The difference is where it runs and whom it acts as.

- **Lists are its database.** An app reads and writes ordinary items through the PaperGo REST API. It has no tables, files or storage of its own.
- **Workflows are its backend.** Anything that must hold whoever enters the data, needs more permission than the viewer has, or takes long, is a workflow. Workflows need nothing new for this.
- **The SDK is the same client.** Apps use the client generated from `api/openapi.json` that workflow scripts use, over a browser transport instead of the workflow engine's.

PaperGo is self-hosted by people, families and small organizations, not run as an enterprise app platform. So there is no drag-and-drop builder, no npm, no build step on the author's machine, no second domain and no per-app server code. It is also not a toy: an app is a real React screen with forms, tables, charts and live data, and the runtime is safe to open for every member.

## Apps and workflows side by side

| | Workflow | App |
| --- | --- | --- |
| Source | one `.ts` file, default export `workflow({...})` | one `.tsx` file, default export `app({...})` |
| SDK module | `@papergo/workflow` | `@papergo/app` (same generated client, plus React hooks and UI) |
| Declares | triggers, condition, forms, concurrency | access, parameters form, title and icon |
| Started by | events, schedules, manual starts, calls | a person opening it |
| Runs in | the server's embedded JavaScript engine, in replayed slices | the viewer's browser, in a sandboxed frame |
| Acts as | the version's **author** | the **viewer** |
| Durability | every step journaled, exactly-once writes | none: a screen; what must last is an item write or a workflow run |
| Network | none | none |
| Editor | Monaco, type-checked against `workflow.d.ts`, test runs | Monaco, type-checked against `app.d.ts`, live preview |
| Lifecycle | draft / published / archived, immutable versions | the same |
| Parameters | JSON object, remapped by packages | the same, with a parameters form of domain pickers |
| Travels in | workspace packages | workspace packages |

## Developer experience

### Where apps live

**Workspace settings → Apps** lists the workspace's apps beside **Workflows**. A workspace manager chooses **New app** and picks a starting point: **Blank**, **Entry form and table**, or **Dashboard**. These are example sources that the dialog copies into the editor, not built-ins. Published apps appear to every member in the workspace sidebar under **Apps** and open at `/w/{workspace}/apps/{key}`. The same URL with `?chrome=0` opens the app without the PaperGo shell, for a phone's home screen.

### The source file

```tsx
import { app, useQuery, useCreateItem, Page, Table, ItemForm } from "@papergo/app";

interface Params { list: string; type: string }

export default app<Params>({
  title: "Fuel log",
  icon: "fuel",
  // The form owners fill in when they configure the app. Pickers return IDs,
  // which packages remap, so the source never holds an ID.
  parameters_schema: {
    type: "object",
    required: ["list", "type"],
    properties: {
      list: { type: "string", title: "List", "x-papergo": { kind: "collection" } },
      type: { type: "string", title: "Entry type", "x-papergo": { kind: "content_type" } },
    },
  },
  // Everything the app may touch. The runtime and the server refuse the rest.
  access: (p) => ({ collections: [{ id: p.list, access: "write" }] }),
  render: ({ params }) => {
    const rows = useQuery(params.list, { sort: [{ field: "$id", direction: "desc" }] }, { live: true });
    return (
      <Page title="Fuel log">
        <ItemForm collection={params.list} contentType={params.type} />
        <Table rows={rows.data} columns={["$name", "measured_at", "depth_cm"]} />
      </Page>
    );
  },
});
```

`app({...})` takes:

| Key | Meaning |
| --- | --- |
| `title`, `icon` | the sidebar entry; `icon` names one of the shell's icons |
| `parameters_schema` | the configuration form, a JSON schema with the [domain pickers](../workflows.md#forms) workflow forms already have (`collection`, `content_type`, `item`, `people`, `terms`…) |
| `access` | `collections`: `{ id, access: "read" \| "write" }` in the app's workspace; `workflows`: keys of workflows the app may start (see [actions](#backend-patterns)). A function of the parameters, like workflow triggers |
| `render(ctx)` | returns the React element of the screen; runs only in the browser |

### What the app gets

`render(ctx)` and the `useApp()` hook give:

| Field | Value |
| --- | --- |
| `ctx.api` | the generated REST client, acting as the viewer, limited to the declared access |
| `ctx.params` | the app's parameters |
| `ctx.user` | the viewer's subject, display name and locale, from the shell; the app never calls identity routes |
| `ctx.workspace` | the workspace's ID and name |
| `ctx.app` | the app's ID, key and version, and whether it is a preview |
| `ctx.run(key, input, { itemId })` | starts a declared workflow and resolves with its output; see [actions](#backend-patterns) |
| `ctx.open(target)` | asks the shell to open an item, a collection or a view in PaperGo |
| `ctx.notify(text, { tone })` | a toast in the shell |
| `ctx.log(...)`, `ctx.warn(...)`, `ctx.error(...)` | the preview's console; `console` does the same |

`@papergo/app` also exports what most screens need, so that apps look like PaperGo in light and dark themes without writing CSS:

- **Data hooks:** `useQuery(collection, query, { all, live, surface })` over `POST /v1/resources/{id}/query`; `useView(viewId)`; `useItem(id)`; `useSchema(collection, contentType)` for field definitions; `useCreateItem`, `useUpdateItem` and `useDeleteItem`, which send `If-Match` from what they read; `useRun(key)`. `live: true` refreshes when the list changes. Hooks expose `{ data, error, loading, refresh }`.
- **Layout and controls:** `Page`, `Section`, `Grid`, `Card`, `Stat`, `Button`, `Field`, `Table`, `EmptyState`, `Alert`.
- **Forms from the schema:** `ItemForm` and `FieldInput` render inputs from a content type's field definitions (decimal, currency, datetime, choice, person, term…), validate as the server does, and create or update the item. A form never drifts from the list.
- **Charts:** `LineChart`, `BarChart`, `AreaChart`, `PieChart` and `Sparkline` over plain row arrays, themed and responsive.
- **Formatting:** `formatDecimal`, `formatCurrency` and `formatDate` in the viewer's locale; decimals stay exact strings until the app chooses to compute.

Apps may use any React they like inside these, and the DOM with them; the runtime ships React and nothing else from npm.

### Editor and preview

The app editor reuses the workflow editor's Monaco setup with JSX: completion, hover documentation and type errors against `app.d.ts` and React's types, for ES2022 and the DOM. Beside the code is a **live preview**: the current source, compiled by the server without saving (`POST /v1/apps/{id}/preview`), running in the same sandboxed frame members get, as the editor's user, against real data. Writes in the preview are real, as they are in workflow [test runs](../workflows.md#testing). A badge says so. A console under the preview shows `ctx.log`, failed requests with their status and problem, and uncaught errors with source-mapped stacks. A render error shows in place of the screen, not as a blank frame.

**Save** bundles the source and stores a new immutable version, as workflows do. **Publish** makes the current version the one members open. The **Parameters** tab renders `parameters_schema` as a form. **Versions** lists earlier versions with their sources.

## Backend patterns

An app does three kinds of work through workflows. All three use workflows as they exist, and one needs a small socket.

1. **Reactive: the app writes items, a workflow derives.** The app creates or updates an item; a workflow on `item.created` / `item.updated` computes values, checks consistency or notifies people. The rule holds whatever wrote the item: the list screen, MCP, another app. This is the default pattern, and the [diesel tracker](use-case-diesel-analysis.md) uses only this one.
2. **Actions: the app calls a workflow and waits for its output.** `ctx.run(key, input, { itemId })` starts a declared workflow's manual run with an `Idempotency-Key`, waits for it through `GET /v1/workflow-runs/{id}?wait=25`, and resolves with its output or rejects with its error. The run acts as the workflow's author. This suits work the viewer may not do directly, several writes that belong together, AI completions, OCR or anything that returns an operation. A run on an item needs the viewer's write access to the item, as manual runs do today. A run on no item needs workspace manage today. Socket S5 lets an owner open such a workflow to members.
3. **Summaries: a scheduled workflow precomputes.** For lists too large to read in the browser, a workflow on a schedule writes summary items, such as one per month, and the app reads those.

## Execution

### Saving

1. `internal/script.Bundle` gains a TSX mode: esbuild's Go API, one file, the automatic JSX runtime, imports limited to `react`, `react/jsx-runtime` and `@papergo/app`, which resolve to globals of the runtime bundle and are not bundled. Any other import is refused with its line, as a workflow's is.
2. The server evaluates the bundle once in the existing engine to read its declaration, as workflow `__manifest` does. It provides inert stand-ins for `react` and `@papergo/app`, and never calls `render`. Module scope must therefore not touch the DOM, which the editor's types and the save error say.
3. It validates the declaration: the collections exist in the workspace, the workflow keys exist, and the parameters match `parameters_schema`, with pickers checked against the data and the saver's permissions as workflow forms are.
4. It stores the version: source, bundle, bundle hash and the normalized declaration.

### Opening

```
Shell (PaperGo page, trusted)                    Frame (sandboxed, opaque origin)
──────────────────────────────                   ────────────────────────────────
GET /v1/apps/{id}           → metadata, declared access, parameters
GET /v1/apps/{id}/bundle    → published version's bundle (viewer's credentials)
<iframe sandbox="allow-scripts allow-forms" src="/app-frame/{runtime-hash}">
                                                 loads the runtime (React, @papergo/app, UI, charts)
                                    ◄── ready
init {bundle, params, user, workspace, theme, locale, access} ──►
                                                 evaluates the bundle, renders ctx
                                    ◄── request {id, method, url, headers, body}
checks the app request policy, then fetch()
as the viewer, with PaperGo-App: {app}:{version}
response {id, status, headers, body}  ──►
                                    ◄── subscribe {collections}
forwards matching /v1/events frames  ──►
```

- **The frame document** at `/app-frame/{runtime-hash}` is the same static file for every app: the runtime bundle and a loader. It is public, immutable and cached for a year, like the web app's hashed assets. Its response header is `Content-Security-Policy: sandbox allow-scripts allow-forms; default-src 'none'; script-src 'self' blob:; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self'; connect-src 'none'; form-action 'none'; frame-ancestors 'self'`. The header sandboxes the document even when someone opens the URL directly.
- **The app's code** never travels as a URL the frame fetches. The shell reads the bundle with its own credentials and posts it in. This works in every authentication mode (local session cookie, trusted proxy, OIDC bearer in the page) without giving the frame any credential.
- **The bridge** is `postMessage` between the shell and that frame only. The shell accepts messages whose `source` is the frame's window, and the frame accepts messages only from its parent. Request and response bodies are JSON text, and binary bodies are transferred `ArrayBuffer`s.
- **Live updates** use the shell's existing `/v1/events` connection; an app never opens its own, so the four streams per user are not spent by apps. The shell forwards `item` frames of declared collections, plus `resync`, after which `live` hooks refetch.
- **Versions.** A frame keeps the version it opened with. When a newer version is published, the shell shows "This app was updated" with a reload button.
- **Errors.** An uncaught error or rejected promise in the frame is posted to the shell. Members see the app's error screen with a retry; managers see the message and stack.

### Who an app acts as

Workflows act as their author, so the owner decides what automation may do. Apps act as the **viewer**: an app is a screen, and a member who may not write a list cannot write it through an app either. The effective access of every app request is

```
viewer's scopes and ACLs  ∩  the app's declared collections (read or write)  ∩  the app route policy
```

The **app route policy** (`sdk/app-scope.json`, beside the workflow `scope.json`) is an allowlist, not a denylist. It covers the collection's items and folders, queries and groups, views and their queries, content types and fields, item comments, people, terms and keywords, the runs the app started, and starting declared workflows. Identity, tokens, sign-in, organization, administration, portability, storage, events, `$batch`, and workflow and app management are outside it.

The shell checks the route policy, because only the route is visible to it. Whether an item ID belongs to a declared collection is something only the server knows. So the server enforces the declared collections through the `PaperGo-App` header, the same narrowing that [limited access tokens](../identity.md) apply to their places (socket S3). It also records the app in audit entries, as "via Diesel tracker", the way an access token is named.

### Threats this answers

| Threat | Answer |
| --- | --- |
| An app author makes an administrator's browser act with the administrator's rights | The frame has an opaque origin: no cookie, no bearer, no access to the shell's DOM or storage. Its requests are limited to its declared collections and the route policy, whoever views it. |
| An app sends data to the internet | `connect-src 'none'`, `form-action 'none'`, no external images or fonts, no top-level navigation or pop-ups (not in `sandbox`). |
| An app reaches another list, workspace or the admin API | The shell's route allowlist and the server's place limit, both from the declaration validated on save. |
| An app pretends to be PaperGo to phish for a password | The shell frames every app with its title and an "App" label. The frame cannot navigate the top window or draw outside its frame. |
| An app floods the server | At most 6 requests in flight per open app, queued by the bridge; the server's ordinary limits and quotas apply. |

## Limitations

| Area | Limit |
| --- | --- |
| Source | one file of at most 1 MiB, importing only `react`, `react/jsx-runtime` and `@papergo/app` |
| Browser | evergreen browsers; no `localStorage`, `sessionStorage`, IndexedDB or cookies (the origin is opaque); no `alert`/`confirm` (use the UI kit's dialogs); no pop-ups, downloads, clipboard, camera, geolocation or top-level navigation |
| Network | none: PaperGo through `ctx.api` only, and nothing else |
| Data reach | the declared collections of the app's own workspace; reading other workspaces is out of scope |
| Queries | filters and sorts use indexed fields only, as for any client; pages of at most 500; groups count, they do not sum |
| Size of data | aggregation happens in the browser; plan for at most about 10,000 rows (20 pages) per screen, and use [summaries](#backend-patterns) beyond that |
| Writes | one item per request, with `If-Match`; a conflict is an error the hooks surface |
| Files | no upload or download in version one |
| Actions | workflow output of at most 16 KiB; a few hundred milliseconds to seconds to start; not meant per keystroke; a run on no item needs workspace manage until S5 |
| State | an app's state lasts as long as its tab; what must last is an item |
| Offline | none; a request without a connection fails visibly |
| Audience | every member who can read the workspace sees its published apps; their data access is still their own |

## The SDK

`sdk/` becomes one package with a shared core and two entry points:

- **Shared:** the generated client (`src/client/`), the request builders (`src/builders.ts`), `HostParseNode` and the request adapter. The adapter is already asynchronous and sends every request through a pluggable host, which is the only seam apps need.
- **`@papergo/workflow`:** the workflow engine's host, `workflow()`, `ctx.wait`, `ctx.ask` and the rest, unchanged.
- **`@papergo/app`:** a host that posts requests over the bridge; `app()`; the hooks, UI kit, charts and formatting.

`npm --prefix sdk run build` then also writes `dist/app-runtime.js` (React, the app SDK, UI kit and charts, which Go embeds and serves at `/app-frame/{hash}`), `dist/app.d.ts` (the app SDK's declarations, the client limited to the app route policy, and React's types for the editor), and the app runtime's third-party notices for release archives. React's version is the web app's. The charts come from one vendored library chosen for its React API and MIT licence; Recharts is the candidate.

## Server

- **Module.** `internal/apps`, a feature module registered through `httpapi.Module`, like `internal/documentpreview`. It owns the app routes, compiling and the frame document, and contributes the app route policy and the `PaperGo-App` narrowing through socket S3.
- **Storage.** `apps` and `app_versions` in the DAL schema masters, owned by the apps module, as `blob_previews` belongs to previews (socket S4). Their columns mirror `workflows` and `workflow_versions`: workspace, key, name, description, state, parameters, current and published version, ETag version, and per version the source, bundle, bundle hash, normalized declaration and author. Deleting a workspace deletes its apps.
- **Packages.** The two tables join the workspace package's owned tables, so apps travel with the lists and workflows they use, and the package's UUID remapping covers their parameters.
- **Workflows cannot manage apps:** `/v1/apps/**` and `/v1/workspaces/*/apps` join the workflow `scope.json` exclusions.

| Method | Route | Purpose |
| --- | --- | --- |
| GET / POST | /v1/workspaces/{id}/apps | List or create apps: `{"key", "name", "description", "source", "parameters", "state"}` |
| GET / PUT / DELETE | /v1/apps/{id} | Read, replace (If-Match), delete (If-Match) |
| GET | /v1/apps/{id}/versions | Versions with their sources, newest first |
| GET | /v1/apps/{id}/bundle | The published version's bundle and declaration (`version` for a manager's draft) |
| POST | /v1/apps/{id}/preview | Compile a source without saving: bundle and declaration, or the problems |
| GET | /app-frame/{hash} | The sandboxed frame document with the runtime |

| Action | Requires |
| --- | --- |
| See and open published apps | workspace `read` |
| Create, edit, publish, delete, preview drafts | workspace `manage` |
| Anything an app does | what the viewer may do, within the app's declaration |

## Extension-first assessment

**Can apps be a pure extension?** The app feature itself is a module: its routes, compiler, runtime, frame, editor page and the SDK entry. It does not change how items, queries, views or workflows behave. But today the core has no hook for five things it needs. Each is generic and has other uses.

| # | Socket | Why apps need it | Other uses |
| --- | --- | --- | --- |
| S1 | **Feature discovery for the web shell.** The server reports which feature modules it runs; the shell shows their pages and sidebar sections only then. | The apps pages appear only where the module runs. | Pages of other optional modules, such as search, which a deployment can turn off. |
| S2 | **Sandboxed frame host and SDK host seam.** A shell component that runs a module-served bundle in a sandboxed frame and forwards its requests and events; the SDK split into a shared core and per-runtime hosts. | It is the only safe way to run owner-written JavaScript in front of other members. | Custom view layouts (a chart layout), item panels, field renderers, rich task forms. |
| S3 | **Request-scoped place limits with per-place read/write**, set by a module from a request header and named in audit. It generalizes the places of limited access tokens. | Only the server knows which collection an item ID belongs to. | Scoped MCP connections, embedded views, integrations acting for a user. |
| S4 | **Module tables in the DAL masters and workspace packages.** `apps` and `app_versions`, owned by the apps module, registered as package-owned tables. | Versioned source and bundle per app, deleted with the workspace, and carried in packages. The activity store cannot do this: a record holds at most 1 MiB of JSON for source and bundle together, expires, carries leases, and is not in packages. | Any future module with versioned workspace configuration. |
| S5 | **Member-startable manual workflows.** A manual trigger option, `starters: "members"`, lets workspace members start a standalone run that acts as its author. | App [actions](#backend-patterns) that have no item. | Buttons in other clients, integrations, MCP. |

S1–S4 are needed for the first release; S5 can follow it. Server-side sums and averages in queries are deliberately not proposed: apps aggregate in the browser, and summaries cover larger lists.

## Out of scope

- A drag-and-drop builder or no-code designer
- npm dependencies, multi-file apps or a build step on the author's machine
- App-owned server code or storage; workflows and lists are both
- Network access from apps; public, anonymous or embedded-elsewhere apps
- Native mobile apps; the runtime is responsive and opens without the shell on a phone
- File upload and download in version one

## Delivery

1. **Sockets S1–S4**, each with its tests, OpenAPI contract and docs.
2. **SDK split** and `@papergo/app` with the bridge host and hooks, tested against a fake shell as the workflow SDK's builders are tested today.
3. **`internal/apps`**: routes, TSX bundling, declaration reading and validation, frame document, packages.
4. **Web:** Apps settings page, editor with live preview and console, sidebar entries, frame host and bridge, update banner.
5. **UI kit and charts**, themed in both themes and both locales.
6. **The diesel tracker** as `samples/diesel-package`: list, workflow and app ([use case](use-case-diesel-analysis.md)).
7. **S5** when an app needs an action without an item.

## Decisions needed

1. Approve sockets S1–S4 for the first release, and S5 for later.
2. Apps act as the viewer, and workflows remain the only way to act as someone else.
3. Recharts as the vendored chart library, or another.
4. Version one without file upload and download.
