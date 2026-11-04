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
| D8 | the person who measures | be asked to measure when 15 days have passed since the last reading, and answer with the reading itself | readings happen regularly without anyone keeping a calendar |
| D9 | the people who buy | be asked to fill the barrel when it falls below a level the owner chooses, and answer with the purchase itself | we fill it up before it runs dry, and the purchase is recorded |

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

## Backend: three workflows

| Workflow | Starts on | Does |
| --- | --- | --- |
| `diesel-levels` | a reading or purchase created or changed | litres, fill %, price per litre and the consistency flag (D2, D5, D6) |
| `diesel-measure-reminder` | every morning, and every new reading | a measuring task when the last reading is 15 days old (D8) |
| `diesel-refill` | `diesel-levels` finishing a reading, and every new purchase | a refill task when the level falls below the threshold (D9) |

They are three workflows rather than one, so the owner can turn the reminder or the refill task off, or change who gets them, without touching the arithmetic. All three compile with the server's workflow compiler and type-check against the SDK as written here.

### Levels

`diesel-levels` reacts to `item.created` and `item.updated` of both types in the Diesel list. Its parameters are the barrel: `list`, `capacity_l`, `height_cm` and `orientation`. Parameters are remapped when the workspace package is imported elsewhere, so nobody edits IDs.

- **Reading:** compute `level_liters` and `fill_percent`. Then read the previous reading and the purchases between the two. If the new level exceeds the previous level plus what was bought, plus a 5 L tolerance, set `flag = check`.
- **Purchase:** `price_per_liter = total_price / bought_liters`.
- **Writes back** with `If-Match`, and only when a value actually changes. Its own write raises `item.updated` again, finds nothing to change and stops. Event depth stops any loop at 5 levels regardless.
- **Recalculate:** a manual trigger on a selected item re-runs it after the owner changes the barrel (D5).
- **Returns** `{ level_liters, fill_percent, previous_level_liters }` for a reading. The run's output reaches the workflows that follow it through the `wf.diesel-levels.completed` event, which the refill task uses.

Values are exact decimal strings in PaperGo, so the script computes in tenths of a litre and thousandths of a currency unit before writing.

This is the **reactive** backend pattern of [workspace apps](workspace-apps.md#backend-patterns). The app only writes items. A reading entered from the list screen, over MCP by an assistant, or by another app gets its litres too, and the formula lives in one script the owner can read and change.

### Measuring reminder

Every morning at 08:00 in the owner's time zone, the reminder looks at the last reading. When it is `every_days` (15) old or more, and no reminder is open, it creates a **task** for `assignees`. The task's form asks for the centimetres on the dipstick and, optionally, when they were measured: left empty, it is the moment of the answer. Answering it files the reading, so the person never opens the list or the app: the task in their Tasks screen is the data entry. `diesel-levels` then converts it like any other reading.

- **15 days after the last reading, not on fixed dates.** Someone who measures on day 10 is next asked on day 25. A fixed calendar (`cron: "0 8 1,16 * *"`) would ask even after a fresh reading. The daily check is cheap: one query, and nothing written on most days.
- **A reading filed any other way closes the reminder.** The same workflow starts on every new reading and cancels the open `diesel_measure` task. The run waiting on that task ends quietly.
- **One reminder at a time.** Workflow concurrency applies only to runs on items, and a schedule run has none, so the script checks for an open task itself.
- **Nothing nags.** The task stays open until someone measures. It is due two days after it was created, which shows it as overdue but does not repeat it. Assignees are told when it is created, as for every task.
- **Assignees** are a parameter: a person, `group:<id>`, `role:owners` or `role:members`. Packages carry neither people nor groups, so the package ships `["role:owners"]`, and the owner names the person after installing.

```ts
// Every morning, asks someone to dip the barrel when the last reading is
// older than every_days. The task's form is the reading: answering it
// files the reading, and a reading filed any other way closes the task.
// The task stays open until then; nothing asks twice.
import { OperationError, json, workflow } from "@papergo/workflow";

interface Params {
  list: string;
  reading_type: string;
  every_days: number;
  // People, group:<id>, role:owners or role:members.
  assignees: string[];
  time_zone: string;
}

const KEY = "diesel_measure";
const DAY = 24 * 3600 * 1000;

export default workflow<unknown, unknown, Params>({
  triggers: (p) => [
    { type: "schedule", cron: "0 8 * * *", time_zone: p.time_zone },
    { type: "item.created", collection_id: p.list, content_type_id: p.reading_type },
  ],
  async run(ctx) {
    const p = ctx.params;
    const api = ctx.api;
    const open = await api.v1.workspaces.byId(ctx.run.workspace_id).tasks.get({ queryParameters: { key: KEY, status: "open" } });

    if (ctx.trigger?.kind === "item") {
      // Someone measured: close the open reminder, whose run then ends.
      for (const task of open?.value ?? []) await api.v1.tasks.byId(task.id!).cancel.post();
      return;
    }
    // Concurrency applies to runs on items only, so a schedule run checks
    // for the reminder still waiting itself.
    if (open?.value?.length) return;

    const last = await api.v1.resources.byId(p.list).query.post(
      { query: { contentTypeId: p.reading_type, sort: [{ field: "measured_at", direction: "desc" }] }, select: ["measured_at"] },
      { queryParameters: { top: 1 } },
    );
    const at = last?.value?.[0]?.values?.additionalData?.measured_at;
    const now = new Date();
    if (at && now.getTime() - new Date(String(at)).getTime() < p.every_days * DAY) return;

    let answer;
    try {
      answer = await ctx.ask<{ depth_cm: number; measured_at: string }>({
        key: KEY,
        title: "Measure the diesel barrel",
        description: at ? `The last reading was on ${String(at).slice(0, 10)}.` : "There is no reading yet.",
        assignees: p.assignees,
        priority: "normal",
        dueAt: new Date(now.getTime() + 2 * DAY),
        inputSchema: {
          type: "object",
          required: ["depth_cm"],
          properties: {
            depth_cm: { type: "number", title: "Diesel on the dipstick (cm)", minimum: 0 },
            measured_at: { type: "string", format: "date-time", title: "Measured at", "x-papergo-default": "now" },
          },
        },
        outcomes: [{ key: "measured", label: "Save reading", style: "primary" }],
      });
    } catch (e) {
      if (e instanceof OperationError) return "measured elsewhere";
      throw e;
    }

    const depth = Number(answer.input!.depth_cm).toFixed(1);
    const when = answer.input!.measured_at;
    await api.v1.resources.byId(p.list).children.post({
      kind: "item",
      name: `Reading ${when.slice(0, 10)} · ${depth} cm`,
      contentTypeId: p.reading_type,
      values: json({ measured_at: when, depth_cm: depth }),
    });
    return "measured";
  },
});
```

### Refill task

When `diesel-levels` finishes a reading, `diesel-refill` reads its output. If the level is below `threshold_l`, it creates a high-priority task for `buyers`, linked to the reading. The task's form is the purchase: litres, total price, optionally when (left empty, the moment of the answer) and the supplier. Answering it files the purchase, and `diesel-levels` then works out its price per litre. It is the measuring reminder's pattern, applied to buying.

- **No separate notification.** Assigning a task already tells its assignees in PaperGo, so the task is the alert and the record at once.
- **One refill task at a time.** The task stays open until someone buys. Readings that stay low find it open and add nothing.
- **A purchase filed any other way closes it.** The same workflow starts on every new purchase and cancels the open `diesel_refill` task, from the app, the list or the measuring reminder alike.
- **The next one comes after a refill.** Once the task is answered or cancelled, the next reading below the threshold asks again.
- **Buyers** follow the reminder's rule: `["role:owners"]` in the package, a person or group set after installing.

```ts
// When a reading falls below threshold_l, asks the buyers to fill the
// barrel. The task's form is the purchase: answering it files the purchase,
// and a purchase filed any other way closes the task.
import { OperationError, json, workflow } from "@papergo/workflow";

interface Params {
  list: string;
  reading_type: string;
  purchase_type: string;
  threshold_l: number;
  // People, group:<id>, role:owners or role:members.
  buyers: string[];
}

// What diesel-levels returns for a reading.
interface Levels {
  level_liters: string;
  fill_percent: string;
  previous_level_liters: string | null;
}

const KEY = "diesel_refill";

export default workflow<unknown, unknown, Params>({
  triggers: (p) => [
    { type: "wf.diesel-levels.completed", collection_id: p.list, content_type_id: p.reading_type },
    { type: "item.created", collection_id: p.list, content_type_id: p.purchase_type },
  ],
  async run(ctx) {
    const p = ctx.params;
    const api = ctx.api;
    const open = await api.v1.workspaces.byId(ctx.run.workspace_id).tasks.get({ queryParameters: { key: KEY, status: "open" } });

    if (ctx.trigger?.kind === "item") {
      // Someone bought diesel: close the open refill task, whose run then ends.
      for (const task of open?.value ?? []) await api.v1.tasks.byId(task.id!).cancel.post();
      return;
    }
    const levels = ctx.run.event.data.outputs as Levels | undefined;
    // Not low, or already asked: the open task stays until someone buys.
    if (!levels || Number(levels.level_liters) >= p.threshold_l || open?.value?.length) return;

    let answer;
    try {
      answer = await ctx.ask<{ bought_liters: number; total_price: number; purchased_at: string; supplier?: string }>({
        key: KEY,
        title: `Fill the diesel barrel: ${levels.level_liters} L left`,
        description: `The barrel is at ${levels.fill_percent} %, below ${p.threshold_l} L.`,
        itemId: ctx.itemId,
        assignees: p.buyers,
        priority: "high",
        inputSchema: {
          type: "object",
          required: ["bought_liters", "total_price"],
          properties: {
            bought_liters: { type: "number", title: "Litres bought", minimum: 0 },
            total_price: { type: "number", title: "Total price", minimum: 0 },
            purchased_at: { type: "string", format: "date-time", title: "Bought at", "x-papergo-default": "now" },
            supplier: { type: "string", title: "Supplier", maxLength: 200 },
          },
        },
        outcomes: [{ key: "bought", label: "Save purchase", style: "primary" }],
      });
    } catch (e) {
      if (e instanceof OperationError) return "bought elsewhere";
      throw e;
    }

    const input = answer.input!;
    const liters = Number(input.bought_liters).toFixed(1);
    const price = Number(input.total_price).toFixed(2);
    await api.v1.resources.byId(p.list).children.post({
      kind: "item",
      name: `Purchase ${input.purchased_at.slice(0, 10)} · ${liters} L`,
      contentTypeId: p.purchase_type,
      values: json({ purchased_at: input.purchased_at, bought_liters: liters, total_price: price, ...(input.supplier ? { supplier: input.supplier } : {}) }),
    });
    return "bought";
  },
});
```

### How people hear about it

Both workflows reach people through tasks, inside PaperGo: assigning a task notifies its assignees, through the bell and its unread count, the Notifications page, the Tasks screen, and live updates while PaperGo is open. PaperGo sends no email or push, by design: people act on the tasks where the data lives.

### What the owner sets after installing

| Workflow | Parameter | Ships as | Set to |
| --- | --- | --- | --- |
| `diesel-levels` | `capacity_l`, `height_cm`, `orientation` | 200, 88, `upright` | the barrel's size and position |
| `diesel-measure-reminder` | `every_days`, `assignees`, `time_zone` | 15, `["role:owners"]`, `UTC` | how often, who measures, where |
| `diesel-refill` | `threshold_l`, `buyers` | 50, `["role:owners"]` | when to ask, and whom |

The list and type IDs in all three are set by the package and remapped on import.

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
| `diesel-levels`, `diesel-measure-reminder`, `diesel-refill` | ordinary workflows, work today |
| Diesel tracker screen | one app, `diesel-tracker.tsx`, declaring read/write access to the Diesel list and nothing else |
| Distribution | one [app package](workspace-apps.md#app-packages), `diesel.zip`: the workspace **Diesel**, the list and its types and views, the workflow and the app. `requires` is empty, since nothing needs AI, OCR or search |
| Example data | an optional manual workflow, **Load example data**, that creates a season of readings and purchases so the dashboard shows something at once |
| After install | the owner sets the [parameters](#what-the-owner-sets-after-installing) of the three workflows, as the readme says; the app shows a setup message until the barrel's size is set |

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
2. Ship the package without the app first, while the app runtime is built? Everything in it, the three workflows included, works today.
