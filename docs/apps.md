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

The server then loads the code with stand-ins for those modules to read the parameters form. Loading must finish within two seconds and must not throw; the server has no browser, so `window`, `document` and `Intl` belong inside `render`. A source that does not compile, exports no `app({ render })` or declares an invalid form is refused with 422 and the reason.

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
| `ctx.format` | `number`, `decimal`, `currency`, `date`, `dateTime` and `relative` in the viewer's preferences; `parseDecimal` reads a typed decimal with the viewer's decimal separator, by the rules of PaperGo's own fields, and returns its canonical text, such as `"41.5"`; a date without a time stays on its day, in any time zone |
| `ctx.open({ itemId } \| { taskId } \| { listId })` | Opens it in PaperGo; an app cannot navigate itself |

| Hook | What it does |
| --- | --- |
| `useQuery(listId, query, { all, live, top, select, compact })` | The list's items that match a [collection query](application-foundation.md) (`filter`, `sort`, `contentTypeId`, `search`), as plain JSON; `all` reads every page, 500 items a request unless `top` says otherwise; `live` reads again when one of the list's items changes; `compact` reads [compact items](application-foundation.md), under half the size: identity, version and values, whether an item is archived, and the built-in columns `select` names |
| `useAggregates(listId, { query, groups, aggregates, sort }, { live, top })` | Figures the server works out of the matching items ([aggregates](application-foundation.md)): `count`, `sum`, `avg`, `min` and `max` of fields, for all of them or in `groups` of up to four `{ field, interval, timeZone }`, a date or date-time by `day`, `week`, `month` or `year` in `timeZone`. Each group holds `keys`, one for each of `groups`. It reads every group, or with `top` the first that many in the order of `sort`; figures that change while their pages are read are read again from the first page. A chart of two years of hourly readings reads 730 days, not 17,520 items |
| `useItem(id, { live })` | One item |
| `useApp()` | The context, in components below `render` |

The kit draws in PaperGo's theme, light or dark as the viewer's:
- **Layout:** `Card` (with actions or a legend beside its title), `Grid`, `Toolbar` and `Modal`.
- **Figures:** `Stat` and `Gauge`, a half-circle of how full something is.
- **Charts:** `LineChart` and `BarChart`, drawn as SVG, with `Legend` pills. `LineChart` draws series over numbers or dates, with markers, shaded bands (such as seasons), reference lines and the ticks it is given.
- **Data:** `Table`.
- **Controls:** `Button`, `Notice`, `NumberField` (a decimal typed with a phone's numeric keyboard, with an optional live hint; `signed` adds a ± button that turns the typed number negative, as those keyboards have no minus), `TextField`, `DateField` and `DateTimeField` (dates picked and shown in the viewer's time zone, as `ctx.format` shows them).

**Language.** The kit's own words follow the viewer's language preference, as PaperGo's web app does: Spanish for any `es` language, English otherwise. They are what screen readers say for a `Modal`'s close button and a signed `NumberField`'s sign button, what an empty `Table` says unless its `empty` says otherwise, and what an empty chart says. Everything else an app shows is its own text; `ctx.user.preferences.language` tells it which language to write in.

**Phones.** The kit lays out for touch up to 640 pixels wide (`narrowWidth`), and `useNarrow()` tells an app when it is shown that narrow:
- **Toolbar:** it becomes a bar fixed to the bottom of the app, in equal parts, under the thumb, and the content keeps clear of it.
- **Charts:** they draw at their real width, so their text stays legible, and keep their ticks apart. Tapping a point, a marker or a bar shows what it is below the chart, since a phone has no hover.
- **Tables:** they scroll sideways when they must, keep values on one line and leave out the columns marked `optional`.
- **Touch targets:** buttons and fields are at least 44 pixels high, and padding is tighter.

`json()` wraps free-form values, such as an item's, for the generated client, and `plain()` turns its untyped answers into JSON. An error in the app shows inside its screen.

## How it runs

People open a workspace's published apps from the sidebar, below its lists. **Apps** in the main navigation, at `/launcher`, lists every app they may open in all their workspaces, grouped by workspace: typing narrows them by name, description or workspace and Enter opens the first; the search palette (Ctrl K) lists them too. Those who write apps add and change them in **Workspace settings → Apps**. The app page gives the app the whole screen below the top bar, with a one-line header, and the app is its only scroll area; on a phone it runs edge to edge. The editor chooses the app's [icon](#icon) and checks the source against `@papergo/app` and React as it is typed. Once a version is saved, it shows the parameters form with its pickers.

The app page puts the app in a sandboxed frame:

1. The page reads the app's compiled code with the viewer's login and writes the frame's one document: the runtime, the app's code and its start, inline.
   - The frame has `sandbox="allow-scripts"`, so it has no origin of its own and cannot read the page's storage, where the login is kept.
   - Its document's CSP allows only those inline scripts and styles, and images from `data:` and `blob:`. It can make no network requests and post no forms, and blocks WebRTC in the browsers that support the `webrtc` directive.
   - PaperGo's page allows frames to show only the documents it writes (`frame-src 'none'`), so the frame cannot navigate anywhere. A frame that loads another document all the same is stopped.
2. Before the app's code runs, the runtime asks the page for a message port, and the page answers that first request only. Every API request the app makes travels over that port.
3. The page sends each request as the viewer and returns the status, the body, and the `Content-Type`, `ETag`, `Location` and `Retry-After` headers. The app sets only `Accept`, `Content-Type`, `If-Match`, `If-None-Match`, `Idempotency-Key` and `Prefer`.
4. The page passes on the live changes of the items the viewer may see in the app's workspace, so live queries read again, and changes of its theme, so the app follows without restarting. Each change says where the page last saw the item, so a live figure of a list reads again when an item leaves it or changes type; an item the app saw that moves to another workspace is told as removed, without saying where it went.

An app does what its viewer may, through the API only, and never holds a login, so it cannot act as the viewer once the page is closed.

**Refused requests.** The page refuses some requests whoever views, with 403 `app_refused`, so an administrator who opens an app does not let it change who administers the organization:
- **Any method:** `/v1/identity` (users, groups, roles, tokens, WebDAV credentials), `/v1/organization`, `/v1/portability`, `/v1/audit`, `/v1/storage`, `/v1/events`, `/v1/mcp`, `/v1/$batch` and the apps routes.
- **Anything but reads:** a workflow's definition (`/v1/workflows/{id}`, `/v1/workspaces/{id}/workflows`, its built-ins), workspace members, permissions, and a list's integrations, which have the server fetch from the network; and installed [solutions](solutions.md), which an app may read but not plan, apply or remove.

An app still starts workflow runs, answers and updates tasks, and reads and writes items.

## Icon

An app has an icon: a glyph in white on a color. The launcher, the sidebar and the app page show it, and it is the app's icon on a phone's home screen.

- **`icon`** names the glyph, one of: `app-window`, `banknote`, `barcode`, `bell`, `bike`, `book-open`, `bookmark`, `briefcase`, `building`, `calculator`, `calendar`, `camera`, `car`, `chart-column`, `chart-line`, `chart-pie`, `clipboard-list`, `clock`, `cloud`, `coffee`, `credit-card`, `database`, `droplet`, `dumbbell`, `file-text`, `flag`, `flame`, `folder`, `fuel`, `gamepad-2`, `globe`, `graduation-cap`, `hammer`, `hand-coins`, `heart`, `house`, `image`, `inbox`, `key`, `landmark`, `leaf`, `list-checks`, `lock`, `mail`, `map-pin`, `music`, `package`, `pencil`, `phone`, `piggy-bank`, `pill`, `plane`, `printer`, `qr-code`, `receipt`, `scale`, `scan-line`, `search`, `settings`, `shield`, `shopping-cart`, `signature`, `square-check`, `star`, `stethoscope`, `sun`, `tag`, `thermometer`, `truck`, `user`, `users`, `utensils`, `wallet`, `wrench`, `zap`. They are [Lucide](https://lucide.dev) icons. An app without one shows the initials of its name, up to two.
- **`color`** is the icon's color as `#rrggbb`. An app without one gets a color from its name, the same every time.

The server draws the icons, at `/pwa/icon-180.png`, `/pwa/icon-192.png`, `/pwa/icon-512.png` and `/pwa/icon-maskable-512.png`, from the `name`, `icon` and `color` in the address. The addresses need no sign-in and read nothing stored: an icon shows only what its address says.

## Installing on a phone

PaperGo and each app can be installed on a phone's home screen, as separate apps, from the browser:

- **PaperGo** installs from any page but an app's, and opens at the launcher. The launcher has an **Install** button.
- **An app** installs from its own page, with the install button in its header, and opens at that page with the app's name and icon. Its window shows the app alone, without PaperGo's menu and top bar. What the app opens elsewhere in PaperGo, such as a task or a list item, shows as a page outside the app, which the phone lets the person close to return.

The button shows where the browser offers to install (Chrome, Edge and Samsung Internet on Android and on computers). On iPhone and iPad it explains Safari's **Share → Add to Home Screen**. Other browsers install from their own menu.

Installing needs HTTPS, as every deployment but a local one has (see [reverse proxies](reverse-proxies.md#public-origin)). What a browser installs is the web manifest the page names: `/pwa/papergo.webmanifest`, or on an app's page `/pwa/app.webmanifest` with the app's workspace, ID, name, icon and color in its address. Like the icons, a manifest is made from its address alone and PaperGo asks no sign-in for it, because a browser's installer does not send PaperGo's. Behind a proxy that authenticates every request, the page has the browser send the proxy's credentials with the manifest and its icons, so `/pwa/` stays behind the same Access List as the rest (see [reverse proxies](reverse-proxies.md)).

An installed app is the web app in its own window, so it needs the network and a sign-in:

- **Signing in** happens in the installed window and returns to the app. With OIDC sign-in, each start of the app signs in again, which the identity provider answers without asking while its own session lasts. A local sign-in lasts 12 hours.
- **iPhone and iPad** keep each installed app's sign-in apart from Safari's and from other installed apps'.
- **Changes** to an app's name or icon reach an installed copy when the phone next reads the manifest, which Android does from time to time while the app is used; on iPhone and iPad the app is installed again.

## Who may do what

| Action | Needs |
| --- | --- |
| See and open published apps | read access to the workspace |
| Create, change, publish and delete apps; see drafts, sources and versions | manage access to the workspace and the `app.manage` scope |

An app acts as everyone who opens it, so `app.manage` is not granted to the Member role; administrators have it (see [identity](identity.md)). Limited access tokens cannot hold it.

Creating, changing and deleting an app are audited as `app.create`, `app.update` and `app.delete` in the workspace's audit trail, with the app's ID, key and version.

## Copying a workspace

A [workspace package](workspace-portability.md) carries the workspace's apps: their key, name, description, icon, color, state and parameters, and every version with its source.
- **Parameters.** The values of the parameters form's pickers are references: the lists, items, content types and terms they select become those of the copy, and people keep their accounts. Other values are the app's own and travel as they are, even when they hold an ID.
- **Outside references.** A picker that selects a list or item outside the workspace, or an item a configuration-only package leaves out, fails the export with the name of what it selects; so does an ID in the current version's source, in any letter case. An import refuses a package whose pickers select anything it does not carry, or something other than what they pick, such as a collection picker that selects an item.
- **Code.** Importing compiles each app's current version again from its source, before the import's transaction opens, so an app never runs code that came in the package. A current version that no longer compiles fails the import, naming the app. IDs of the copied workspace in the source, such as a picker limited to one list, become the copy's, in its code and form too.
- **Earlier versions** are history that never runs: they keep their source, code and form, and the IDs they name are neither followed nor checked.

## API

| Method | Route | Purpose |
| --- | --- | --- |
| GET | `/v1/apps` | The apps the caller may see in every workspace, by name, without their source or parameters form, each with `workspace_name`, for the launcher |
| GET / POST | `/v1/workspaces/{id}/apps` | List the apps the caller may see, by name, without their source or parameters form; or create one: `{"name", "description", "icon", "color", "source", "parameters", "state", "key"}` |
| GET / PUT / DELETE | `/v1/apps/{id}` | Read; replace name, description, icon, color, state, parameters and source (`If-Match`); delete with its versions (`If-Match`) |
| GET | `/v1/apps/{id}/versions` | Versions with their sources and forms, newest first |
| GET | `/v1/apps/{id}/bundle` | The current version's compiled code, for the app page; its `ETag` is the code's hash and `PaperGo-App-Version` the version |

- **Keys.** An app's `key` comes from its name unless creation gives one, and never changes. Keys and names are unique in a workspace.
- **States.** A new app is a `draft` unless it is created `published`.
- **Versions.** A replacement with a new source becomes the next version; one with the same source keeps the current version.
- **Not for scripts.** The workflow SDK's client leaves these routes out.

The complete contract is in [OpenAPI](../api/openapi.json).

## Samples

The [diesel tracker](../samples/diesel/README.md) is a complete app: a [solution](solutions.md) with a list, four workflows and a dashboard that estimates and forecasts a heating-oil tank from its dipstick readings, purchases and heating seasons, beside the indoor and outdoor temperatures. The [receipts desk](../samples/receipts/README.md) uploads receipt photos from a phone, shrunk on the phone to what the model reads, for a workflow that reads their lines and keeps each receipt as a PDF.
