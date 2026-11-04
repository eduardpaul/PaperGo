# Use case analysis: diesel tracker

Status: proposal; the complete sample of [workspace apps](workspace-apps.md) phase 0, which describes how apps are written, run and limited. This document covers only what the diesel tracker needs and how it maps onto that architecture.

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

One list, **Diesel**, with two content types, `diesel_reading` (**Reading**) and `diesel_purchase` (**Purchase**). One list keeps the history in one place and lets the app read everything with one query. The keys carry the `diesel_` prefix because content type and field keys are organization-wide; the sample's fields carry it too (`diesel_depth_cm` and so on), which this document leaves out for reading.

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

Saved views: **Readings** (table, type Reading, `measured_at` desc) and **Purchases** (table, type Purchase, `purchased_at` desc). A calendar view groups by one date field, and the two types keep theirs apart, so there is none. D7 is met by the existing list screens with no extra work.

## Backend: three workflows

| Workflow | Starts on | Does |
| --- | --- | --- |
| `diesel-levels` | a reading or purchase created or changed | litres, fill %, price per litre and the consistency flag (D2, D5, D6) |
| `diesel-measure-reminder` | every morning, and every new reading | a measuring task when the last reading is 15 days old (D8) |
| `diesel-refill` | `diesel-levels` finishing a reading, and every new purchase | a refill task when the level falls below the threshold (D9) |

They are three workflows rather than one, so the owner can turn the reminder or the refill task off, or change who gets them, without touching the arithmetic. All three compile with the server's workflow compiler and type-check against the SDK as written here.

### Levels

`diesel-levels` reacts to `item.created` and `item.updated` of both types in the Diesel list. Its parameters are the barrel: `list`, `capacity_l`, `height_cm` and `orientation`. The workspace package carries the list and type IDs, which an import remaps, so nobody edits IDs.

- **Reading:** compute `level_liters` and `fill_percent`. Then read the previous reading and the purchases between the two. If the new level exceeds the previous level plus what was bought, plus a 5 L tolerance, set `flag = check`.
- **Purchase:** `price_per_liter = total_price / bought_liters`.
- **Writes back** with `If-Match`, and only when a value actually changes. Its own write raises `item.updated` again, finds nothing to change and stops. Event depth stops any loop at 5 levels regardless.
- **Recalculate all:** a standalone manual trigger (`access: "manage"`) converts every reading again, a page at a time with `continueAsNew`, after the owner changes the barrel (D5). The app offers it to owners as a button.
- **A reading the owner checked stays checked.** `flag` has a third value, `reviewed`, which the owner sets on a reading they confirmed; the workflow never changes it back to `check`.
- **A purchase entered late re-checks the reading after it**, so a forgotten purchase filed afterwards clears that reading's `check`.
- **Nothing to change, nothing returned.** A run that finds the values already right returns no output, so its own write-back does not ask the refill workflow again.
- **Returns** `{ level_liters, fill_percent, previous_level_liters }` for a reading. The run's output reaches the workflows that follow it through the `wf.diesel-levels.completed` event, which the refill task uses.

Values are exact decimal strings in PaperGo, so the script computes in tenths of a litre and thousandths of a currency unit before writing.

The app only writes items; the workflow derives. A reading entered from the list screen, over MCP by an assistant, or by another app gets its litres too, and the formula lives in one script the owner can read and change.

### Measuring reminder

Every morning at 08:00 in the owner's time zone, the reminder looks at the last reading. When it is `every_days` (15) old or more, and no reminder is open, it creates a **task** for `assignees`. The task's form asks for the centimetres on the dipstick and, optionally, when they were measured: left empty, it is the moment of the answer. Answering it files the reading, so the person never opens the list or the app: the task in their Tasks screen is the data entry. `diesel-levels` then converts it like any other reading.

- **15 days after the last reading, not on fixed dates.** Someone who measures on day 10 is next asked on day 25. A fixed calendar (`cron: "0 8 1,16 * *"`) would ask even after a fresh reading. The daily check is cheap: one query, and nothing written on most days.
- **A reading filed any other way closes the reminder.** The same workflow starts on every new reading and cancels the open `diesel_measure` task with a reason naming the reading, which the cancelled task shows. Its "task assigned" notification goes with it, and the run waiting on it ends quietly. A task answered at the same moment is no longer open; the cancel's 409 is ignored.
- **One reminder at a time.** The task is [unique](../workflows.md#tasks): while one is open, the daily check is refused with `task_open` and asks nothing.
- **Nothing nags.** The task stays open until someone measures. It is due two days after it was created, which shows it as overdue but does not repeat it. Assignees are told when it is created, as for every task.
- **Assignees** are a parameter: a person, `group:<id>`, `role:owners` or `role:members`. A workspace package can name neither people nor groups of the instance it is imported into, so it ships `["role:owners"]`, and the owner names the person after importing.

```ts
// Every morning, asks someone to dip the barrel when the last reading is
// older than every_days. The task's form is the reading: answering it
// files the reading, and a reading filed any other way closes the task.
// The task stays open until then; nothing asks twice.
import { OperationError, json, workflow } from "@papergo/workflow";
import type { PaperGoClient } from "@papergo/workflow";

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

// close cancels an open task for reason. A task answered or cancelled
// meanwhile is no longer open (409), which is what close wants anyway.
async function close(api: PaperGoClient, id: string, reason: string): Promise<void> {
  try {
    await api.v1.tasks.byId(id).cancel.post({ reason });
  } catch (e) {
    if ((e as { responseStatusCode?: number }).responseStatusCode !== 409) throw e;
  }
}

export default workflow<unknown, unknown, Params>({
  triggers: (p) => [
    { type: "schedule", cron: "0 8 * * *", time_zone: p.time_zone },
    { type: "item.created", collection_id: p.list, content_type_id: p.reading_type },
  ],
  async run(ctx) {
    const p = ctx.params;
    const api = ctx.api;

    if (ctx.trigger?.kind === "item") {
      // Someone measured: close the open reminder, whose run then ends.
      const open = await api.v1.workspaces.byId(ctx.run.workspace_id).tasks.get({ queryParameters: { key: KEY, status: "open" } });
      for (const task of open?.value ?? []) await close(api, task.id!, `Measured: ${ctx.trigger.item.name}.`);
      return;
    }

    const last = await api.v1.resources.byId(p.list).query.post(
      { query: { contentTypeId: p.reading_type, sort: [{ field: "measured_at", direction: "desc" }] }, select: ["measured_at"] },
      { queryParameters: { top: 1 } },
    );
    const at = last?.value?.[0]?.values?.additionalData?.measured_at;
    const now = new Date();
    if (at && now.getTime() - new Date(String(at)).getTime() < p.every_days * DAY) return;

    let answer;
    try {
      // Unique: while the last reminder is open, asking again is refused.
      answer = await ctx.ask<{ depth_cm: number }>({
        key: KEY,
        unique: true,
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
          },
        },
        outcomes: [{ key: "measured", label: "Save reading", style: "primary" }],
      });
    } catch (e) {
      if ((e as { code?: string }).code === "task_open") return "already asked";
      if (e instanceof OperationError) return "measured elsewhere";
      throw e;
    }

    const depth = Number(answer.input!.depth_cm).toFixed(1);
    const when = new Date().toISOString();
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
- **One refill task at a time.** The task is unique and stays open until someone buys. A reading that stays low is refused with `task_open`, also when two runs ask at once, and [updates](../workflows.md#tasks) the open task's title and description to its own level instead, so the task always shows how much is left.
- **A purchase filed any other way closes it.** The same workflow starts on every new purchase and cancels the open `diesel_refill` task with a reason naming the purchase, from the app, the list or anywhere else. Its notification goes with it, and a 409 from a task answered meanwhile is ignored.
- **The next one comes after a refill.** Once the task is answered or cancelled, the next reading below the threshold asks again.
- **Buyers** follow the reminder's rule: `["role:owners"]` in the package, a person or group set after importing.

```ts
// When a reading falls below threshold_l, asks the buyers to fill the
// barrel. The task's form is the purchase: answering it files the purchase,
// and a purchase filed any other way closes the task.
import { OperationError, json, workflow } from "@papergo/workflow";
import type { PaperGoClient } from "@papergo/workflow";

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

// close cancels an open task for reason. A task answered or cancelled
// meanwhile is no longer open (409), which is what close wants anyway.
async function close(api: PaperGoClient, id: string, reason: string): Promise<void> {
  try {
    await api.v1.tasks.byId(id).cancel.post({ reason });
  } catch (e) {
    if ((e as { responseStatusCode?: number }).responseStatusCode !== 409) throw e;
  }
}

export default workflow<unknown, unknown, Params>({
  triggers: (p) => [
    { type: "wf.diesel-levels.completed", collection_id: p.list, content_type_id: p.reading_type },
    { type: "item.created", collection_id: p.list, content_type_id: p.purchase_type },
  ],
  async run(ctx) {
    const p = ctx.params;
    const api = ctx.api;

    if (ctx.trigger?.kind === "item") {
      // Someone bought diesel: close the open refill task, whose run then ends.
      const open = await api.v1.workspaces.byId(ctx.run.workspace_id).tasks.get({ queryParameters: { key: KEY, status: "open" } });
      for (const task of open?.value ?? []) await close(api, task.id!, `Bought: ${ctx.trigger.item.name}.`);
      return;
    }
    const levels = ctx.run.event.data.outputs as Levels | undefined;
    if (!levels || Number(levels.level_liters) >= p.threshold_l) return;

    const title = `Fill the diesel barrel: ${levels.level_liters} L left`;
    const description = `The barrel is at ${levels.fill_percent} %, below ${p.threshold_l} L.`;
    let answer;
    try {
      // Unique: while the refill task is open, asking again is refused.
      answer = await ctx.ask<{ bought_liters: number; total_price: number; purchased_at: string; supplier?: string }>({
        key: KEY,
        unique: true,
        title,
        description,
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
      const problem = e as { code?: string; openTaskId?: string };
      if (problem.code === "task_open") {
        // Already asked: the open task shows this reading's level instead.
        const task = api.v1.tasks.byId(problem.openTaskId!);
        const current = await task.get();
        try {
          await task.patch({ title, description }, { headers: { "If-Match": `"${current!.version}"` } });
        } catch (err) {
          // Answered (409), or changed by another reading (412), meanwhile.
          const status = (err as { responseStatusCode?: number }).responseStatusCode;
          if (status !== 409 && status !== 412) throw err;
        }
        return "updated";
      }
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

### What the owner sets after importing

| Workflow | Parameter | Ships as | Set to |
| --- | --- | --- | --- |
| `diesel-levels` | `capacity_l`, `height_cm`, `orientation` | 200, 88, `upright` | the barrel's size and position |
| `diesel-measure-reminder` | `every_days`, `assignees`, `time_zone` | 15, `["role:owners"]`, `UTC` | how often, who measures, where |
| `diesel-refill` | `threshold_l`, `buyers` | 50, `["role:owners"]` | when to ask, and whom |

The list and type IDs in all three come with the workspace package. `time_zone` ships as `UTC`; the README asks the owner to set theirs.

## Dashboard: figures and formulas

The list is tiny: about two readings a week and one purchase a month is under 150 items a year. The app reads the whole list with one paged query and computes everything in the browser, for decades of use.

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

In phase 0 the sample is a workspace package and an app, installed one after the other (see [the diesel sample](workspace-apps.md#the-diesel-sample)):

| Piece | What it is |
| --- | --- |
| Diesel list, two content types, two views | `diesel.zip`, a workspace package, imported as a new workspace |
| `diesel-levels`, `diesel-measure-reminder`, `diesel-refill` | ordinary workflows in the same package, authored by the importer |
| Diesel tracker screen | `diesel-app.tsx`, an app added in Workspace settings → Apps; its parameters (the list and the two types) are chosen with pickers |
| After importing | the owner sets the [parameters](#what-the-owner-sets-after-importing) of the three workflows |

The screen reads the whole list with one live query, computes the figures above, and has the two forms. It finds the viewer's open reminder or refill task with `GET /v1/me/tasks?key=…&status=open` and shows "A reading is due" or "Diesel is low, buy some", which opens the task with `ctx.open`. Owners also get **Recalculate all**, which starts the levels workflow's standalone manual run.

```tsx
import { app, useQuery, json } from "@papergo/app";

export default app({
  parameters: {
    type: "object",
    required: ["list", "reading", "purchase"],
    properties: {
      list: { type: "string", title: "Diesel list", "x-papergo": { kind: "collection" } },
      reading: { type: "string", title: "Reading type", "x-papergo": { kind: "content_type" } },
      purchase: { type: "string", title: "Purchase type", "x-papergo": { kind: "content_type" } },
    },
  },
  render(ctx) {
    const rows = useQuery(ctx.params.list, {}, { all: true, live: true });
    const add = (depth: string) =>
      ctx.api.v1.resources.byId(ctx.params.list).children.post({
        kind: "item", name: "Reading", contentTypeId: ctx.params.reading,
        values: json({ measured_at: new Date().toISOString(), depth_cm: ctx.format.parseDecimal(depth) }),
      });
    // compute the figures above from rows.data, then render the tiles, charts and forms
    return <Dashboard ctx={ctx} rows={rows.data} onAddReading={add} />;
  },
});
```

## Phase 0 sample

The sample takes these corrections from the review of this analysis:
- **Fields carry the `diesel_` prefix**, so they never collide with another package's fields in the organization catalog.
- **No calendar view**, since readings and purchases keep separate date fields.
- **Recalculate all**, the `reviewed` flag and the re-check after a late purchase, described under [Levels](#levels).
- **Refills are asked once**: `diesel-refill` has `concurrency: "skip"`, its task is unique, and a low reading updates the open task to its level.
- **"Now" estimates today's level**, the last reading less the average rate times the days since, and says when that reading was taken.
- **"Days left" needs two readings** and a positive rate; otherwise it says why it has no estimate.
- **Both barrel geometries**, upright and horizontal.

## Product notes

- **Measure before refilling.** Ask for an optional reading right after the **Add purchase** form ("Dip it now?"). Two readings around a refill make that interval exact and catch mistakes at once.
- **One barrel.** Version one keeps the barrel in the workflow's parameters. Several tanks (a tractor tank, a heating tank) would become a third content type, **Tank**, and a lookup field on readings and purchases. The data model allows it without breaking anything, so it can wait until someone asks for it.
- **Currency** is fixed on the `total_price` field (`options.currency_code`), which the package ships as EUR; the README says how to change it before the first purchase.
- **Without the app**, the package alone is already useful: the list, the workflows and the views work on their own.

## Decisions taken

1. Barrel geometry: upright and horizontal, both in version one.
