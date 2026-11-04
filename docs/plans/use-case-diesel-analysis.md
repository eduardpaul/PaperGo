# Use case analysis: diesel tracker

Status: proposal. This is the first concrete app for [workspace apps](workspace-apps.md), which describes how apps are written, run and limited. This document covers only what the diesel tracker needs and how it maps onto that architecture.

## The problem

A farm or house keeps diesel in a barrel. Two things happen:

1. **Measure.** Someone dips a stick into the barrel and reads how many centimetres of diesel are left.
2. **Buy.** Someone buys N litres for a price on a date, and pours them in.

They want to open one screen, type a number, and see how much diesel is left, how fast it goes, what it costs, and when the barrel will be empty.

## User stories

| # | As | I want to | So that |
| --- | --- | --- | --- |
| D1 | anyone in the workspace | enter a dipstick reading in cm, in two taps on a phone | recording it is quicker than not recording it |
| D2 | anyone | see the litres for that reading at once | I don't have to do the arithmetic |
| D3 | anyone | record a purchase: litres, total price, date, optionally the supplier | spend is tracked |
| D4 | the owner | see the current level, litres per day and month, spend per month, price per litre over time, and the projected empty date | I know when to buy and what diesel costs us |
| D5 | the owner | set the barrel's size once and fix it later | every reading, including old ones, converts correctly |
| D6 | the owner | be told when a reading makes no sense (higher than the last one with no purchase in between) | a typo or a forgotten purchase doesn't corrupt the figures |
| D7 | anyone | correct a wrong entry in the normal list screen | the app is not the only way in |

## Converting centimetres to litres

For an upright cylindrical barrel the level is linear in the height, so it is a rule of three. If the full height `H` cm holds `C` litres:

```
litres = depth_cm × C / H        (clamped to 0 … C)
fill % = litres / C × 100
```

Example: a 200 L drum, 88 cm high, reading 41 cm → 41 × 200 / 88 = 93.2 L, 46.6 %.

A barrel lying on its side is **not** linear: the litres follow the circular segment, `C × (θ − sin θ) / 2π` with `θ = 2·acos(1 − 2h/D)`. Because the conversion is a workflow script, it is a parameter (`orientation: "upright" | "horizontal"`) and a few lines, not a product change. Version one should ship both. Otherwise a horizontal tank silently reads up to about 6 % of its capacity wrong (12 L on a 200 L drum) when it is a fifth or four-fifths full.

## Data model

One list, **Diesel**, with two content types, `diesel_reading` (**Reading**) and `diesel_purchase` (**Purchase**). One list keeps the history in one place and lets the app read everything with one query. The keys carry the package's prefix because content type keys are organization-wide; see [app packages](workspace-apps.md#building-one).

| Content type | Field | Type | Notes |
| --- | --- | --- | --- |
| **Reading** | `measured_at` | datetime, required, indexed | defaults to now in the app |
| | `depth_cm` | decimal, scale 1, required, min 0 | what the person types |
| | `level_liters` | decimal, scale 1 | written by the workflow |
| | `fill_percent` | decimal, scale 1 | written by the workflow |
| | `flag` | choice: `ok`, `check`, indexed | written by the workflow (D6) |
| **Purchase** | `purchased_at` | datetime, required, indexed | |
| | `bought_liters` | decimal, scale 1, required, min 0 | |
| | `total_price` | currency (workspace default, e.g. EUR), required | |
| | `price_per_liter` | decimal, scale 3 | written by the workflow |
| | `supplier` | text | optional |

The two types use different field keys (`level_liters` and `bought_liters`), so a level is never added to a quantity bought by mistake. Both timestamps are datetimes, not dates. Readings are taken on the same day as refills, and the consumption maths needs to know which came first.

Saved views: **Readings** (table, type Reading, `measured_at` desc), **Purchases** (table, type Purchase, `purchased_at` desc) and **Calendar** (both). D7 is met by the existing list screens with no extra work.

## Backend: one reactive workflow

`diesel-levels` reacts to `item.created` and `item.updated` of both types in the Diesel list. Its parameters are the barrel: `list`, `capacity_l`, `height_cm` and `orientation`. Parameters are remapped when the workspace package is imported elsewhere, so nobody edits IDs.

- **Reading:** compute `level_liters` and `fill_percent`. Then read the previous reading and the purchases between the two. If the new level exceeds the previous level plus what was bought, plus a 5 L tolerance, set `flag = check`.
- **Purchase:** `price_per_liter = total_price / bought_liters`.
- **Writes back** with `If-Match`, and only when a value actually changes. Its own write raises `item.updated` again, finds nothing to change and stops. Event depth stops any loop at 5 levels regardless.
- **Recalculate:** a manual trigger on a selection re-runs it after the owner changes the barrel (D5).

Values are exact decimal strings in PaperGo, so the script computes in tenths of a litre and thousandths of a currency unit before writing.

This is the **reactive** backend pattern of [workspace apps](workspace-apps.md#backend-patterns). The app only writes items. A reading entered from the list screen, over MCP by an assistant, or by another app gets its litres too, and the formula lives in one script the owner can read and change.

## Dashboard: figures and formulas

The list is tiny: about two readings a week and one purchase a month is under 150 items a year. The app reads the whole list with one paged query and computes everything in the browser, within the [data limits](workspace-apps.md#limitations) of apps for decades of use.

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

The screen also has two forms, **Add reading** and **Add purchase**. They are large inputs that default to now and work on a phone. Saving creates the item. The dashboard updates when the workflow has written the litres, through the app's live query, usually within a second.

## How it maps onto workspace apps

| Piece | What it is |
| --- | --- |
| Diesel list, two content types, three views | ordinary workspace configuration, works today |
| `diesel-levels` | an ordinary workflow, works today |
| Diesel tracker screen | one app, `diesel-tracker.tsx`, declaring read/write access to the Diesel list and nothing else |
| Distribution | one [app package](workspace-apps.md#app-packages), `diesel.zip`: the workspace **Diesel**, the list and its types and views, the workflow and the app. `requires` is empty, since nothing needs AI, OCR or search |
| Example data | an optional manual workflow, **Load example data**, that creates a season of readings and purchases so the dashboard shows something at once |
| After install | the owner sets `capacity_l`, `height_cm` and `orientation` in the workflow's parameters, as the readme says; the app shows a setup message until they are set |

```tsx
import { app, useQuery, Page, Stat, LineChart, BarChart, ItemForm } from "@papergo/app";

interface Params { list: string; reading: string; purchase: string }

export default app<Params>({
  parameters_schema: {
    type: "object",
    required: ["list", "reading", "purchase"],
    properties: {
      list: { type: "string", title: "Diesel list", "x-papergo": { kind: "collection" } },
      reading: { type: "string", title: "Reading type", "x-papergo": { kind: "content_type" } },
      purchase: { type: "string", title: "Purchase type", "x-papergo": { kind: "content_type" } },
    },
  },
  access: (p) => ({ collections: [{ id: p.list, access: "write" }] }),
  render: ({ params }) => {
    const rows = useQuery(params.list, { sort: [{ field: "$id", direction: "asc" }] }, { all: true, live: true });
    // compute the figures above from rows.data, then:
    return <Page title="Diesel">…<ItemForm collection={params.list} contentType={params.reading} fields={["measured_at", "depth_cm"]} />…</Page>;
  },
});
```

## Product notes

- **Measure before refilling.** Ask for an optional reading right after the **Add purchase** form ("Dip it now?"). Two readings around a refill make that interval exact and catch mistakes at once.
- **One barrel.** Version one keeps the barrel in the workflow's parameters. Several tanks (a tractor tank, a heating tank) would become a third content type, **Tank**, and a lookup field on readings and purchases. The data model allows it without breaking anything, so it can wait until someone asks for it.
- **Currency** comes from the workspace default, which already exists.
- **Without the app**, the list, the workflow and the views are already useful as a package today, and they can ship first as `samples/diesel-package`.

## Decisions needed

1. Barrel geometry for version one: upright only, or upright and horizontal (recommended).
2. Ship the package without the app first, while the app runtime is built?
