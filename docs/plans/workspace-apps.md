# Workspace apps (plan)

Status: phase 0 implemented. [Apps](../apps.md) describes them as they are; the [diesel tracker](../../samples/diesel/README.md) runs on them as the complete sample ([analysis](use-case-diesel-analysis.md)). This plan keeps the decisions and what comes later.

Decisions taken: apps run in a sandboxed frame and act with the viewer's own credentials; writing apps needs the new `app.manage` scope; apps join workspace packages later. Packaging lists, workflows and apps together ("solutions") comes after phase 0.

## What an app is

An **app** is a React screen that belongs to a workspace, as a workflow does. Members open it from the sidebar. It reads and changes the workspace's data through the PaperGo REST API, as the person viewing it.

An app is built like a workflow and never creates lists. It points at lists that already exist through its parameters:

| | Workflow | App |
| --- | --- | --- |
| Belongs to | a workspace | a workspace |
| Source | one TypeScript file, compiled by the server | one TSX file, compiled by the server |
| Declares | triggers, forms, concurrency | its parameters form |
| Points at data with | `parameters` | `parameters`, chosen with pickers |
| Versions | every save is an immutable version; draft or published | the same |
| Edited in | Workspace settings → Workflows | Workspace settings → Apps |
| Acts as | the author of its version | the person viewing it |
| Creates lists | no | no |

An app works with workflows through the existing API: it starts their manual runs and reads the results, and it finds, answers and updates their tasks. A workflow decides who may start it by hand with its manual trigger's `access`.

## Writing an app

```tsx
import { app, useQuery, Stat } from "@papergo/app";

export default app({
  parameters: {
    type: "object",
    required: ["list"],
    properties: {
      list: { type: "string", title: "Diesel list", "x-papergo": { kind: "collection" } },
    },
  },
  render(ctx) {
    const rows = useQuery(ctx.params.list, { sort: [{ field: "measured_at", direction: "desc" }] }, { live: true });
    const last = rows.data[0];
    return <Stat label="Now" value={last ? ctx.format.number(last.values.level_liters) + " L" : "No reading yet"} />;
  },
});
```

- **`parameters`** is a [form](../workflows.md#forms): Workspace settings shows it with pickers, and saving checks the values against the workspace and the person who saves.
- **`render(ctx)`** is a React component.

| `ctx` | What it is |
| --- | --- |
| `ctx.api` | The generated REST client, the same as the workflow SDK's, sent as the viewer |
| `ctx.request(method, path, { body })` | Any API route by path, as the viewer |
| `ctx.params` | The app's parameters |
| `ctx.app` | The app's ID, key, name and running version |
| `ctx.user` | Who is viewing: subject, name, and preferences (language, time zone, number and date formats) |
| `ctx.workspace` | Where: ID, name and the viewer's role |
| `ctx.format` | Numbers, decimals, currencies and dates in the viewer's preferences; `parseDecimal` reads typed decimals with their decimal separator |
| `ctx.open(target)` | Opens an item, a task or a list in PaperGo; the app cannot navigate itself |

| Hook | What it does |
| --- | --- |
| `useQuery(list, query, { live, all })` | A collection query; `all` reads every page; `live` reads it again when an item of the list changes |
| `useItem(id, { live })` | One item |
| `useApp()` | The context, for components below `render` |

The runtime also ships a small kit in PaperGo's theme: `Card`, `Grid`, `Toolbar`, `Modal`, `Stat`, `Gauge`, `LineChart` (bands, reference lines, ticks), `BarChart`, `Legend`, `Table`, `Button`, `NumberField`, `TextField`, `DateField`, `DateTimeField` and `Notice`. Everything else is the app's own React.

## How it runs

1. The app page reads the app's compiled bundle with the viewer's login (`GET /v1/apps/{id}/bundle`).
2. It starts a sandboxed frame:
   - `sandbox="allow-scripts"`, so the frame has no origin of its own and cannot read PaperGo's storage, where the login is kept;
   - a document that holds the runtime and the bundle inline, under the CSP `default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data: blob:`. The frame has no network, no form posts and no navigation.
3. The app's API calls travel to the page by `postMessage`. The page sends each one as the viewer, with the viewer's login, and returns the status, the body and the `ETag` and `Location` headers.

The app can do what the viewer may, and only through the API. It never sees a login, so it cannot keep acting as the viewer once the page closes.

The page refuses, whoever views:
- paths outside `/v1`;
- with any method, the routes that administer PaperGo or would carry requests past these rules: `/v1/identity`, `/v1/organization`, `/v1/portability`, `/v1/audit`, `/v1/storage`, `/v1/events`, `/v1/mcp`, `/v1/$batch` and the apps routes. Without this, an administrator who opens an app would let it change who administers the organization;
- anything but reads of workflow definitions and built-ins, workspace members, permissions and list integrations, which run as their author, decide who has access, or have the server fetch from the network.

The page passes on the live changes of the items the app's queries read, so `live` queries refresh. Errors in the app show inside its frame, without affecting the page.

## Who may do what

| Action | Needs |
| --- | --- |
| See and open the published apps | read access to the workspace |
| Create, change, publish, delete; open drafts | manage access to the workspace and the `app.manage` scope |

An app acts as everyone who opens it, so `app.manage` is not granted to the Member role: Administrators have it, and organizations grant it as they grant administration. A limited access token cannot hold it.

## Storage and API

Two tables in both DAL schema masters:
- **`apps`**: workspace, key, name, description, state (`draft` or `published`), parameters, current version, who created and last changed it, and an ETag version;
- **`app_versions`**: app, number, source, compiled bundle and its hash, the parameters form, and who saved it.

Deleting an app deletes it and its versions. Apps of a deleted workspace are out of reach, like its other content, and go when it is purged.

| Method | Route | Purpose |
| --- | --- | --- |
| GET / POST | `/v1/workspaces/{id}/apps` | List the apps the caller may see, or create one: `{"name", "description", "source", "parameters", "state"}` |
| GET / PUT / DELETE | `/v1/apps/{id}` | Read, replace (`If-Match`), delete (`If-Match`) |
| GET | `/v1/apps/{id}/versions` | Versions with their sources, newest first |
| GET | `/v1/apps/{id}/bundle` | The current version's compiled code, for the app page |

Creating, changing and deleting an app are audited (`app.create`, `app.update`, `app.delete`).

## Compiling

Saving compiles the source with the esbuild the server embeds, as workflows are compiled:
- **TSX, automatic JSX.** The only imports are `react`, `react/jsx-runtime` and `@papergo/app`, which the runtime provides. Any other import is refused, naming it.
- **One file, up to 1 MiB.**
- **The parameters form** is read by evaluating the bundle in the script engine, with React replaced by stubs, and is checked like any form. A source that does not compile, or declares an invalid form, is refused with the reason.

## Runtime SDK

`@papergo/app` lives in `sdk/` beside the workflow SDK and reuses its generated client and request builders, over a `postMessage` transport. Its build adds two files, which the web app embeds:
- `sdk/dist/app-runtime.js`: React, the runtime and the kit;
- `sdk/dist/app.d.ts`: its declarations, for the editor.

## Web

- **Sidebar:** the workspace's published apps, under its lists.
- **Launcher** (`/launcher`, Apps in the main navigation, and the search palette): every app the viewer may open, in all workspaces, from `GET /v1/apps`.
- **App page:** the frame, the bridge, and the app's name and description.
- **Workspace settings → Apps:**
  - the list of apps, with Add, Publish and Delete;
  - the editor: Monaco with the app's declarations, beside the parameters form;
  - Open, to view a draft.

## The diesel sample

`samples/diesel/` holds the complete sample, built from its sources like the [receipts package](../../samples/receipts-package/README.md):
- **`diesel.zip`**, a workspace package: the Diesel list, its fields, the Reading, Purchase, Heating and Temperature content types, the saved views, and the four workflows (`diesel-levels`, `diesel-measure-reminder`, `diesel-refill`, `diesel-sample-data`);
- **`diesel-app.tsx`**, the dashboard: the figures and charts of the use case, the reading, fill and heating dialogs, and the open reminder or refill task with a link to answer it;
- **a README** with the two install steps:
  1. Import the package.
  2. Add the app with its parameters chosen by the pickers.

  Phase 0 has no way to install both at once: that comes with solutions.

The [use case analysis](use-case-diesel-analysis.md) explains its design.

## Delivery

Each step is a commit with the narrowest tests that prove it:

1. **Scope.** `app.manage` in the scope catalog, for Administrators. Tests: `internal/identity`.
2. **Storage.** The `apps` and `app_versions` tables in both schema masters, the regenerated Bob models, and the repository. Tests: `internal/dal/apps` on both engines.
3. **Compiler.** TSX bundling, the import rules, and reading the parameters form. Tests: `internal/apps`.
4. **Service and routes.** Permissions, versions, audit, the OpenAPI contract and `docs/apps.md`. Tests: `internal/apps` and the apps routes in `internal/httpapi`.
5. **Runtime SDK.** `app()`, the transport, the hooks, `ctx.format`, the kit, and the two build outputs. Tests: the SDK's tests for the app runtime.
6. **Web.** The frame host and bridge with their refusals and live changes, the sidebar, the app page and the settings page. Tests: vitest for the bridge and the pages.
7. **Diesel sample.**
   - The package sources and ZIP, the app and the README.
   - Tests:
     - the sample package test;
     - a `scriptrun` test of the workflows;
     - a browser check that imports the package, adds the app and sees a reading turn into litres on the dashboard.

## Core changes

Only `app.manage` in the scope catalog (approved). The tables, routes, compiler and runtime belong to the apps feature. The bridge and pages are web feature code. It needs no change to workflows, tasks or items: manual trigger `access`, `unique` tasks and task updates already serve it.

## Phase 0 is done when

- A workspace manager with `app.manage` adds, edits, publishes and deletes an app in Workspace settings, choosing its parameters with pickers.
- A member opens it from the sidebar and reads and changes items with their own permissions. What they may not do is refused as it would be anywhere else.
- The app cannot reach the network, read the login, navigate, or call the refused routes.
- The diesel sample installs with its two steps and works end to end:
  - a reading becomes litres;
  - a low level asks the buyers;
  - a purchase closes the task;
  - the dashboard shows the figures of the use case.

## Later

1. **Apps in workspace packages.** This needs a core hook that lets a module export and import its own objects, with the IDs in parameters remapped.
2. **Solutions.** Lists, workflows and apps installed together into an existing workspace, upgraded in place and removed together.
3. **Narrower access.** Limit an app to the lists it declares, or to a short-lived limited credential.
4. **Editor.** Live preview, type checking on save, several files.
5. **A larger kit.** Forms from content types.

## Out of scope

A store, licences or payments; network access from apps; public or anonymous apps; native mobile apps.
