# Apps

An **app** is a React screen that belongs to a workspace, as a [workflow](workflows.md) does. Its people open it from the workspace. It reads and changes the workspace's data through the REST API, as the person viewing it, and never creates lists: it points at lists that exist through its parameters.

| | Workflow | App |
| --- | --- | --- |
| Source | one TypeScript file, compiled by the server | one TSX file, compiled by the server |
| Declares | triggers, forms, concurrency | its parameters form |
| Versions | every save is an immutable version | every new source is an immutable version |
| Acts as | the author of its version | the person viewing it |

## Writing an app

The source's default export is `app()` from `@papergo/app`, with a `render` component and an optional `parameters` [form](workflows.md#forms):

```tsx
import { app } from "@papergo/app";

export default app({
  parameters: {
    type: "object",
    required: ["list"],
    properties: {
      list: { type: "string", title: "Diesel list", "x-papergo": { kind: "collection" } },
    },
  },
  render(ctx) {
    return <p>Readings are kept in {ctx.params.list}.</p>;
  },
});
```

**Compiling.** Saving compiles the source with the server's esbuild:
- TSX with automatic JSX.
- One file of up to 1 MiB.
- It imports only `react`, `react/jsx-runtime` and `@papergo/app`, which the app's runtime supplies; any other import is refused, naming it.

The server then loads the code with stand-ins for those modules to read the parameters form. Loading must finish within two seconds and must not throw. A source that does not compile, exports no `app({ render })` or declares an invalid form is refused with 422 and the reason.

**Parameters** are the values of that form, which the app reads as `ctx.params`. Pickers let the people who set up the app choose the workspace's lists, content types or items instead of typing IDs. Saving checks the values as the person who saves:
- their types and picks always, so a draft can be saved before its lists are chosen;
- the form's required fields once the app is published.

## The runtime

`@papergo/app` is what apps are written against. Its runtime renders the app in the browser, with React 19 and the context `render` receives:

| `ctx` | What it is |
| --- | --- |
| `ctx.api` | The generated REST client, the workflow SDK's, sent as the viewer |
| `ctx.request(method, path, { body, headers })` | Any API route, such as `GET /v1/me/tasks?key=diesel_measure&status=open`, as the viewer; JSON bodies are sent and read as JSON, and a status of 400 or more throws a `RequestError` with the problem's `code` and `detail` |
| `ctx.params` | The app's parameters |
| `ctx.user` | Who is viewing: `subject`, `name` and `preferences` (language, time zone, date, time and number formats) |
| `ctx.workspace` | `id`, `name` and the viewer's `role`: `owner`, `member` or `visitor` |
| `ctx.app` | The app's `id`, `key`, `name` and the `version` running |
| `ctx.format` | `number`, `decimal`, `currency`, `date`, `dateTime` and `relative` in the viewer's preferences; `parseDecimal` reads a typed decimal with the viewer's decimal separator, as PaperGo's own fields do, and returns its canonical text, such as `"41.5"` |
| `ctx.open({ itemId } \| { taskId } \| { listId })` | Opens it in PaperGo; an app cannot navigate itself |

| Hook | What it does |
| --- | --- |
| `useQuery(listId, query, { all, live, top, select })` | The list's items that match a [collection query](application-foundation.md) (`filter`, `sort`, `contentTypeId`, `search`), as plain JSON; `all` reads every page; `live` reads again when one of the list's items changes |
| `useItem(id, { live })` | One item |
| `useApp()` | The context, in components below `render` |

The kit draws in PaperGo's theme, light or dark as the viewer's:
- **Layout:** `Card` (with actions or a legend beside its title), `Grid`, `Toolbar` and `Modal`.
- **Figures:** `Stat` and `Gauge`, a half-circle of how full something is.
- **Charts:** `LineChart` and `BarChart`, drawn as SVG, with `Legend` pills. `LineChart` draws series over numbers or dates, with markers, shaded bands (such as seasons), reference lines and the ticks it is given.
- **Data:** `Table`.
- **Controls:** `Button`, `Notice`, `NumberField` (a decimal typed with a phone's numeric keyboard, with an optional live hint; `signed` adds a ± button that turns the typed number negative, as those keyboards have no minus), `TextField`, `DateField` and `DateTimeField` (dates picked and shown in the viewer's time zone, as `ctx.format` shows them).

**Phones.** The kit lays out for touch up to 640 pixels wide (`narrowWidth`), and `useNarrow()` tells an app when it is shown that narrow:
- **Toolbar:** it becomes a bar fixed to the bottom of the app, in equal parts, under the thumb, and the content keeps clear of it.
- **Charts:** they draw at their real width, so their text stays legible, and keep their ticks apart. Tapping a point, a marker or a bar shows what it is below the chart, since a phone has no hover.
- **Tables:** they scroll sideways when they must, keep values on one line and leave out the columns marked `optional`.
- **Touch targets:** buttons and fields are at least 44 pixels high, and padding is tighter.

`json()` wraps free-form values, such as an item's, for the generated client, and `plain()` turns its untyped answers into JSON. An error in the app shows inside its screen.

## How it runs

People open a workspace's published apps from the sidebar, below its lists. **Apps** in the main navigation, at `/launcher`, lists every app they may open in all their workspaces, grouped by workspace: typing narrows them by name, description or workspace and Enter opens the first; the search palette (Ctrl K) lists them too. Those who write apps add and change them in **Workspace settings → Apps**. The app page gives the app the whole screen below the top bar, with a one-line header, and the app is its only scroll area; on a phone it runs edge to edge. The editor checks the source against `@papergo/app` and React as it is typed. Once a version is saved, it shows the parameters form with its pickers.

The app page puts the app in a sandboxed frame:

1. The page reads the app's compiled code with the viewer's login and writes the frame's one document: the runtime, the app's code and its start, inline.
   - The frame has `sandbox="allow-scripts"`, so it has no origin of its own and cannot read the page's storage, where the login is kept.
   - Its document's CSP allows only those inline scripts and styles, and images from `data:` and `blob:`. It can make no network requests and post no forms.
   - PaperGo's page allows frames to show only the documents it writes (`frame-src 'none'`), so the frame cannot navigate anywhere. A frame that loads another document all the same is stopped.
2. Before the app's code runs, the runtime asks the page for a message port, and the page answers that first request only. Every API request the app makes travels over that port.
3. The page sends each request as the viewer and returns the status, the body, and the `Content-Type`, `ETag`, `Location` and `Retry-After` headers. The app sets only `Accept`, `Content-Type`, `If-Match`, `If-None-Match`, `Idempotency-Key` and `Prefer`.
4. The page passes on the live changes of the items the viewer may see, so live queries read again.

An app does what its viewer may, through the API only, and never holds a login, so it cannot act as the viewer once the page is closed.

**Refused requests.** The page refuses some requests whoever views, with 403 `app_refused`, so an administrator who opens an app does not let it change who administers the organization:
- **Any method:** `/v1/identity` (users, groups, roles, tokens, WebDAV credentials), `/v1/organization`, `/v1/portability`, `/v1/audit`, `/v1/storage`, `/v1/events`, `/v1/mcp`, `/v1/$batch` and the apps routes.
- **Anything but reads:** a workflow's definition (`/v1/workflows/{id}`, `/v1/workspaces/{id}/workflows`, its built-ins), workspace members, permissions, and a list's integrations, which have the server fetch from the network.

An app still starts workflow runs, answers and updates tasks, and reads and writes items.

## Who may do what

| Action | Needs |
| --- | --- |
| See and open published apps | read access to the workspace |
| Create, change, publish and delete apps; see drafts, sources and versions | manage access to the workspace and the `app.manage` scope |

An app acts as everyone who opens it, so `app.manage` is not granted to the Member role; administrators have it (see [identity](identity.md)). Limited access tokens cannot hold it.

Creating, changing and deleting an app are audited as `app.create`, `app.update` and `app.delete` in the workspace's audit trail, with the app's ID, key and version.

## API

| Method | Route | Purpose |
| --- | --- | --- |
| GET | `/v1/apps` | The apps the caller may see in every workspace, by name, each with `workspace_name`, for the launcher |
| GET / POST | `/v1/workspaces/{id}/apps` | List the apps the caller may see, by name, without their source; or create one: `{"name", "description", "source", "parameters", "state", "key"}` |
| GET / PUT / DELETE | `/v1/apps/{id}` | Read; replace name, description, state, parameters and source (`If-Match`); delete with its versions (`If-Match`) |
| GET | `/v1/apps/{id}/versions` | Versions with their sources and forms, newest first |
| GET | `/v1/apps/{id}/bundle` | The current version's compiled code, for the app page; its `ETag` is the code's hash and `PaperGo-App-Version` the version |

- **Keys.** An app's `key` comes from its name unless creation gives one, and never changes. Keys and names are unique in a workspace.
- **States.** A new app is a `draft` unless it is created `published`.
- **Versions.** A replacement with a new source becomes the next version; one with the same source keeps the current version.
- **Not for scripts.** Workflow scripts cannot call these routes.

The complete contract is in [OpenAPI](../api/openapi.json).

## Sample

The [diesel tracker](../samples/diesel/README.md) is a complete app: a workspace package with a list and four workflows, and a dashboard that estimates and forecasts a heating-oil tank from its dipstick readings, purchases and heating seasons, beside the indoor and outdoor temperatures.
