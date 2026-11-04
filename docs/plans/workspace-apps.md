# Workspace apps

Status: proposal. Nothing here is implemented. The sockets in [Core sockets this needs](#core-sockets-this-needs) need approval before any code is written (see the Extension-First Protocol in [AGENTS.md](../../AGENTS.md)).

## The idea

A **workspace app** is a small React screen that a workspace owner writes in PaperGo and the workspace's members open from the sidebar. It has no server code and no storage of its own:

- **Lists are its database.** It reads and writes ordinary items through the PaperGo API: create an item, query a list, read a view.
- **Workflows are its backend.** Anything that must hold whoever enters the data (derived values, checks, notifications, schedules) is a workflow that reacts to the items the app writes. Workflows exist today and need nothing new.
- **The SDK is the same one.** Apps use the client generated from `api/openapi.json` that workflow scripts use, with a browser transport in place of the workflow engine's.

PaperGo is for self-hosting. The target is one person or a family, a farm or a small business running their own instance, not an enterprise app platform. That target sets the scope at both ends. No visual drag-and-drop builder, npm, build pipeline, separate app domain or per-app database. Also no half-measure such as a "dashboard widget" setting that cannot grow into a real screen. One TSX file, the same editing experience as workflows, and a complete, safe runtime.

## Use case: diesel tracker

### What the person does

A farm or house keeps diesel in a barrel. Two things happen:

1. **Measure.** Someone dips a stick into the barrel and reads how many centimetres of diesel are left.
2. **Buy.** Someone buys N litres for a price on a date, and pours them in.

They want to open one screen, type a number, and see how much diesel is left, how fast it goes, what it costs, and when the barrel will be empty.

### User stories

| # | As | I want to | So that |
| --- | --- | --- | --- |
| D1 | anyone in the workspace | enter a dipstick reading in cm, in two taps on a phone | recording it is quicker than not recording it |
| D2 | anyone | see the litres for that reading at once | I don't have to do the arithmetic |
| D3 | anyone | record a purchase: litres, total price, date, optionally the supplier | spend is tracked |
| D4 | the owner | see the current level, litres per day and month, spend per month, price per litre over time, and the projected empty date | I know when to buy and what diesel costs us |
| D5 | the owner | set the barrel's size once and fix it later | every reading, including old ones, converts correctly |
| D6 | the owner | be told when a reading makes no sense (higher than the last one with no purchase in between) | a typo or a forgotten purchase doesn't corrupt the figures |
| D7 | anyone | correct a wrong entry in the normal list screen | the app is not the only way in |

### Converting centimetres to litres

For an upright cylindrical barrel the level is linear in the height, so it is a rule of three. If the full height `H` cm holds `C` litres:

```
litres = depth_cm × C / H        (clamped to 0 … C)
fill % = litres / C × 100
```

Example: a 200 L drum, 88 cm high, reading 41 cm → 41 × 200 / 88 = 93.2 L, 46.6 %.

A barrel lying on its side is **not** linear: the litres follow the circular segment, `C × (θ − sin θ) / 2π` with `θ = 2·acos(1 − 2h/D)`. Because the conversion is a workflow script, it is a parameter (`orientation: "upright" | "horizontal"`) and a few lines, not a product change. Version one should ship both. Otherwise a horizontal tank silently reads up to about 6 % of its capacity wrong (12 L on a 200 L drum) when it is a fifth or four-fifths full.

### Data model (works today)

One list, **Diesel**, with two content types. One list keeps the history in one place and lets the app read everything with one query.

| Content type | Field | Type | Notes |
| --- | --- | --- | --- |
| **Reading** | `measured_at` | datetime, required, indexed | defaults to now in the app |
| | `depth_cm` | decimal, scale 1, required, min 0 | what the person types |
| | `level_liters` | decimal, scale 1 | written by the workflow |
| | `fill_percent` | decimal, scale 1 | written by the workflow |
| | `flag` | choice: `ok`, `check` | written by the workflow (D6) |
| **Purchase** | `purchased_at` | datetime, required, indexed | |
| | `bought_liters` | decimal, scale 1, required, min 0 | |
| | `total_price` | currency (workspace default, e.g. EUR), required | |
| | `price_per_liter` | decimal, scale 3 | written by the workflow |
| | `supplier` | text | optional |

The two types use different field keys (`level_liters` and `bought_liters`), so a level is never added to a quantity bought by mistake. Both timestamps are datetimes, not dates. Readings are taken on the same day as refills, and the consumption maths needs to know which came first.

Saved views: **Readings** (table, type Reading, `measured_at` desc), **Purchases** (table, type Purchase, `purchased_at` desc) and **Calendar** (both). D7 is met by the existing list screens with no extra work.

### Backend: one workflow (works today)

`diesel-levels` reacts to `item.created` and `item.updated` of both types in the Diesel list. Its parameters are the barrel: `list`, `capacity_l`, `height_cm` and `orientation`. Parameters are remapped when the workspace package is imported elsewhere, so nobody edits IDs.

- **Reading:** compute `level_liters` and `fill_percent`. Then read the previous reading and the purchases between the two. If the new level exceeds the previous level plus what was bought, plus a 5 L tolerance, set `flag = check`.
- **Purchase:** `price_per_liter = total_price / bought_liters`.
- **Writes back** with `If-Match`, and only when a value actually changes. Its own write raises `item.updated` again, finds nothing to change and stops. Event depth stops any loop at 5 levels regardless.
- **Recalculate:** a manual trigger on a selection re-runs it after the owner changes the barrel (D5).

Values are exact decimal strings in PaperGo, so the script computes in tenths of a litre and thousandths of a currency unit before writing.

Doing this in a workflow rather than in the app is deliberate. A reading entered from the list screen, over MCP by an assistant, or by another app gets its litres too, and the formula lives in one script the owner can read and change.

### Dashboard: what it shows and how each figure is computed

The list is tiny: about two readings a week and one purchase a month is under 150 items a year. The app reads the whole list with one paged query (`query.all`) and computes everything in the browser, so no server aggregation is needed now or in ten years of use.

Sort readings `R` and purchases `P` by time. For each pair of consecutive readings `Rᵢ₋₁ → Rᵢ`:

```
bought_i   = Σ bought_liters of purchases in (tᵢ₋₁, tᵢ]
consumed_i = level(Rᵢ₋₁) + bought_i − level(Rᵢ)        (an interval with consumed_i < −5 L is flagged, not plotted)
rate_i     = consumed_i / days(tᵢ₋₁, tᵢ)
```

| Tile / chart | Definition |
| --- | --- |
| **Now** (stat tile) | last reading's litres and fill %, its age ("3 days ago") |
| **Days left** (stat tile) | `level_now / average rate over the last 60 days`, and the projected empty date |
| **Average consumption** (stat tile) | litres per day and per month over the last 90 days |
| **Average price** (stat tile) | `Σ total_price / Σ bought_liters` over the last 12 months (weighted, not a mean of prices) |
| **Level over time** (line) | litres per reading, with purchases marked on the time axis |
| **Consumption per month** (bars) | each interval's `consumed_i` split over the months it spans, in proportion to days |
| **Spend per month** (bars) | `Σ total_price` of the purchases in each month |
| **Price per litre** (line/dots) | `price_per_liter` per purchase |
| **Cost of diesel used** (per month) | consumption per month × the weighted average price of the preceding purchases |
| **Needs a look** (list) | readings with `flag = check`, linking to the item |

Every screen also has two forms, **Add reading** and **Add purchase**. They are large inputs that default to now and work on a phone. Saving creates the item, and the dashboard updates when the workflow has written the litres, through the live event stream, usually within a second.

### Product notes

- **Measure before refilling.** Ask for an optional reading right after the **Add purchase** form ("Dip it now?"). Two readings around a refill make that interval exact and catch mistakes at once.
- **One barrel.** Version one keeps the barrel in the workflow's parameters. Several tanks (a tractor tank, a heating tank) would become a third content type, **Tank**, and a lookup field on readings and purchases. The data model allows it without breaking anything, so it can wait until someone asks for it.
- **Currency** comes from the workspace default, which already exists.

## What PaperGo has today, and what it lacks

| Need | Today |
| --- | --- |
| Store readings and purchases | ✅ a list with two content types, typed decimal, currency and datetime fields |
| Litres, price per litre, flags | ✅ a workflow on `item.created` / `item.updated`, running as its author |
| Edit and correct entries | ✅ list screens, views, history, recycle bin |
| Ship it as one installable thing | ✅ a [workspace package](../workspace-portability.md), as [the receipts sample](../../samples/receipts-package/README.md) does |
| A custom screen with forms and charts | ❌ nothing. This is the feature. |

Everything except the screen can be built today with no code change. It can ship first as `samples/diesel-package` to prove the backend half.

## Proposed design

### What an app is

An app is a workspace object with the same shape and life as a workflow: a name, an icon, one TypeScript/TSX source file of up to 1 MiB, `parameters` remapped on package import, immutable **versions**, and a `draft`/`published` state. Members see published apps in the workspace sidebar. Owners edit drafts in an editor beside a live preview. The editor is the existing Monaco setup, type-checking against the app SDK's declarations, as the workflow editor does today.

```tsx
import { app, Stat, LineChart, Form, useQuery } from "@papergo/app";

interface Params { list: string; reading_type: string; purchase_type: string }

export default app<Params>({
  // What this app may touch. The runtime refuses everything else.
  access: (p) => ({ collections: [{ id: p.list, write: true }] }),
  render: ({ api, params }) => {
    const rows = useQuery(params.list, { sort: [{ field: "$id", direction: "asc" }] });
    // …compute the figures above, render Stat, LineChart, Form…
  },
});
```

`app({...})` mirrors `workflow({...})`: the save bundles the source with the esbuild the server already embeds, reads its declarations, and refuses a source that does not compile or declares invalid access.

### The runtime

- **React, a chart component set and a small UI kit** (stat tile, form fields, table, card) ship inside the PaperGo executable as one runtime bundle, like `sdk/dist/sdk.js`. They follow PaperGo's theme tokens in light and dark. Apps import only `react` and `@papergo/app`. Nothing is downloaded, which suits offline and self-hosted installs.
- **The app runs in a sandboxed iframe** (`sandbox="allow-scripts allow-forms"`, no `allow-same-origin`, CSP `default-src 'none'`, no `connect-src`). It gets an opaque origin, never sees the session cookie, and cannot reach the network, PaperGo or anywhere else. This works on one hostname, so self-hosters need no wildcard DNS or second domain.
- **The SDK's transport is a message bridge.** `sdk/src/adapter.ts` already sends every request through a pluggable, asynchronous `host.request`. The app flavour implements it with `postMessage` to the PaperGo page, which forwards the request **as the viewer**, and only when the request is one the app declared (method, path and resources). The server still applies the viewer's ACLs and scopes. An app never does more than both the viewer and its declaration allow.
- **Live updates** come through the same bridge: the page forwards `/v1/events` frames for the declared collections, so `useQuery` refreshes when a workflow writes back.

### Who an app acts as

Workflows act as their **author**, so the owner controls what automation may do. Apps act as the **viewer**: an app is a screen, and a member who may not write the Diesel list cannot write it through the app either. Work that needs more than the viewer has goes into a workflow, which is exactly the "workflows are the backend" rule.

### Packaging

Apps travel in workspace packages with the lists, types, views and workflows they use, with parameters remapped like workflow parameters. The diesel tracker then installs from **Administration → Export and import** in one step, like the receipts sample.

## Extension-first assessment

**Can apps be a pure extension?** Mostly. The app module itself (its routes, the editor, the runtime bundle, the bridge, the diesel sample) belongs in an extension, `extensions/apps`, registered through `httpapi.Module` as `extensions/mcp` is. App definitions and versions can be the module's own durable records in `activitystore.Store`, so the base DAL tables are not touched.

**What it cannot do without core changes**, because the core has no hook for it today, is in the next section.

## Core sockets this needs

Each is a generic primitive, not an app feature. Each also unlocks other things.

| # | Socket | Why apps need it | Who else uses it |
| --- | --- | --- | --- |
| S1 | **Web shell contributions.** The web app reads a catalog of module-contributed workspace pages (label, icon, route, permission) and renders them in the sidebar, each hosting a sandboxed frame. | There is no way today for a module to add a page or sidebar entry to the React shell. | Custom view layouts (a "chart" layout), item-detail panels, extension settings pages such as MCP. |
| S2 | **Sandboxed frame host and request bridge.** A shell component that loads a module-served bundle in a sandboxed iframe and forwards its `postMessage` requests and event frames. A browser transport for the generated SDK, beside the workflow engine's. | It is the only safe way to run owner-written JavaScript in front of other users. In PaperGo's own origin, a member who can write an app could act as any administrator who opens it. | Custom field renderers, rich task forms, any future extension UI. |
| S3 | **Request-scoped resource limits.** The server narrows a request to a declared set of places, the mechanism [limited access tokens](../identity.md) already use, when the request carries an app's declaration. | Defence in depth. The bridge already filters, but the server should enforce the declaration too, not only the page. | Scoped MCP connections, embedded views, integrations that act for a user. |
| S4 | **Portable module records.** Modules register an export/import contributor for their `activitystore` namespace, with ID remapping of declared reference fields. | Without it, apps would not travel in workspace packages, and the diesel tracker could not install in one step. | Any module with workspace configuration: MCP connection settings, future dashboards, integration presets. |

Deliberately **not** proposed:

- **Server-side sums and averages in queries.** `/query/groups` counts only. Apps of this size aggregate in the browser. Add aggregates when an app needs them over a list too large to read.
- **Viewer-started standalone workflows.** Starting a manual workflow with no item needs workspace manage today. The diesel tracker does not need it: its workflow reacts to the items the viewer writes. Revisit for apps whose "submit" has no item.

### The alternative: apps in core

Instead of S4 plus `activitystore`, apps could get their own DAL tables (`apps`, `app_versions`) beside the workflow tables and take part in packages as workflows do. That is simpler to build and query. But it adds base tables for a feature that can live outside the core, which the protocol asks us to avoid. **Recommendation: S4 and an extension.**

## Out of scope

- A drag-and-drop builder or no-code designer
- npm dependencies, multi-file projects or a build step on the author's machine
- App-owned server code or storage. Workflows and lists cover both
- Network access from apps, or public and anonymous apps
- Native mobile apps. The runtime is responsive and works in the phone's browser

## Delivery

1. **Diesel package without an app.** Diesel list, both content types, views and the `diesel-levels` workflow as `samples/diesel-package`. No core change; it proves the data and backend half and is useful at once with the list screens.
2. **Sockets S1–S4**, after approval, each with its tests, OpenAPI contract and docs.
3. **`extensions/apps`.** Definitions, versions, editor with preview, runtime bundle, bridge, `@papergo/app` SDK and docs.
4. **The diesel app** added to the package: the forms and the dashboard above.

## Decisions needed

1. Approve sockets S1–S4, or change them.
2. Apps as an extension on `activitystore` with S4 (recommended), or core tables like workflows.
3. Barrel geometry for version one: upright only, or upright and horizontal (recommended).
4. Build step 1, the diesel package with no app, now?
