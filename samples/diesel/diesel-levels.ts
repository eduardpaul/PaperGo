// Works out what a dipstick reading means: the litres in the tank and how
// full it is, from the tank's size and how the stick is read. A reading
// higher than the one before plus what was bought between them is marked
// check, unless someone marked it reviewed. A purchase gets its price per
// litre, and the reading after it is checked again, so a purchase filed
// late clears that reading. Recalculate all, a manual start for owners,
// converts every reading again after the tank's settings change.
import { json, workflow } from "@papergo/workflow";
import type { PaperGoClient, models } from "@papergo/workflow";

interface Params {
  list: string;
  reading_type: string;
  purchase_type: string;
  capacity_l: number;
  // The height of the tank that holds diesel.
  useful_height_cm: number;
  // From the filling mouth to the bottom of the tank.
  stick_length_cm: number;
  // from_top: the stick measures from the mouth down to the diesel; depth:
  // the wet length of the stick.
  measure: "from_top" | "depth";
  orientation: "upright" | "horizontal";
  // A reading may be this much higher than expected before it is marked check.
  tolerance_l: number;
}

type Item = models.Resource;

/** What a converted reading returns, for workflows that follow this one. */
export interface Levels {
  level_liters: string;
  fill_percent: string;
  previous_level_liters: string | null;
}

const value = (item: Item | undefined, key: string): unknown => item?.values?.additionalData?.[key];
const number = (v: unknown): number | undefined => (v === null || v === undefined || v === "" ? undefined : Number(v));
const time = (v: unknown): number => new Date(String(v)).getTime();
const fixed = (n: number, digits: number): string => n.toFixed(digits);

/** The litres of a stick reading, within the tank. */
export function liters(p: Params, stick: number): number {
  const wet = Math.min(Math.max(p.measure === "depth" ? stick : p.stick_length_cm - stick, 0), p.useful_height_cm);
  if (p.orientation === "horizontal") {
    // A cylinder on its side: the litres follow the circular segment.
    const theta = 2 * Math.acos(1 - (2 * wet) / p.useful_height_cm);
    return (p.capacity_l * (theta - Math.sin(theta))) / (2 * Math.PI);
  }
  return (wet / p.useful_height_cm) * p.capacity_l;
}

async function items(api: PaperGoClient, p: Params, type: string, field: string): Promise<Item[]> {
  const query = { query: { contentTypeId: type as never, sort: [{ field, direction: "asc" as never }] } };
  return (await api.v1.resources.byId(p.list).query.all(query, { queryParameters: { top: 200 } })) as Item[];
}

// write sets changed values of item at its version. A newer change wins:
// its own update event converts it again.
async function write(api: PaperGoClient, item: Item, values: Record<string, unknown>): Promise<boolean> {
  if (Object.keys(values).length === 0) return false;
  try {
    await api.v1.resources.byId(item.id!).patch({ values: json(values) }, { headers: { "If-Match": `"${item.version}"` } });
    return true;
  } catch (e) {
    if ((e as { responseStatusCode?: number }).responseStatusCode === 412) return false;
    throw e;
  }
}

// convert works out a reading's litres, fill and flag against the reading
// before it and the purchases between, and writes what changed.
async function convert(api: PaperGoClient, p: Params, reading: Item, readings: Item[], purchases: Item[]): Promise<Levels | undefined> {
  const stick = number(value(reading, "diesel_stick_cm"));
  if (stick === undefined) return undefined;
  const level = liters(p, stick);
  const at = time(value(reading, "diesel_measured_at"));
  const before = readings.filter((r) => r.id !== reading.id && time(value(r, "diesel_measured_at")) < at).at(-1);
  let previous: number | undefined;
  let flag = value(reading, "diesel_flag") === "reviewed" ? "reviewed" : "ok";
  if (before) {
    const since = time(value(before, "diesel_measured_at"));
    // From its stick with today's settings: after the tank's settings
    // change, the litres stored on it may still be the old ones.
    const beforeStick = number(value(before, "diesel_stick_cm"));
    previous = beforeStick === undefined ? number(value(before, "diesel_level_liters")) ?? 0 : liters(p, beforeStick);
    const bought = purchases
      .filter((b) => time(value(b, "diesel_purchased_at")) > since && time(value(b, "diesel_purchased_at")) <= at)
      .reduce((sum, b) => sum + (number(value(b, "diesel_bought_liters")) ?? 0), 0);
    if (flag !== "reviewed" && level > previous + bought + p.tolerance_l) flag = "check";
  }
  const out: Levels = { level_liters: fixed(level, 1), fill_percent: fixed((level / p.capacity_l) * 100, 1), previous_level_liters: previous === undefined ? null : fixed(previous, 1) };
  const changed: Record<string, unknown> = {};
  if (number(value(reading, "diesel_level_liters")) !== Number(out.level_liters)) changed.diesel_level_liters = out.level_liters;
  if (number(value(reading, "diesel_fill_percent")) !== Number(out.fill_percent)) changed.diesel_fill_percent = out.fill_percent;
  if (value(reading, "diesel_flag") !== flag) changed.diesel_flag = flag;
  return (await write(api, reading, changed)) ? out : undefined;
}

// price works out a purchase's price per litre and writes it when it changed.
async function price(api: PaperGoClient, purchase: Item): Promise<void> {
  const liters = number(value(purchase, "diesel_bought_liters"));
  const total = number(value(purchase, "diesel_total_price"));
  if (!liters || total === undefined) return;
  const per = fixed(total / liters, 3);
  if (number(value(purchase, "diesel_price_per_liter")) !== Number(per)) await write(api, purchase, { diesel_price_per_liter: per });
}

export default workflow<unknown, unknown, Params>({
  triggers: (p) => [
    { type: "item.created", collection_id: p.list, content_type_id: p.reading_type },
    { type: "item.updated", collection_id: p.list, content_type_id: p.reading_type },
    { type: "item.created", collection_id: p.list, content_type_id: p.purchase_type },
    { type: "item.updated", collection_id: p.list, content_type_id: p.purchase_type },
    // Recalculate all, for owners, after the tank's settings change.
    { type: "manual", access: "manage" },
  ],
  // A burst of changes to one item converts it once more at most.
  concurrency: "coalesce",
  async run(ctx) {
    const p = ctx.params;
    const api = ctx.api;
    const readings = await items(api, p, p.reading_type, "diesel_measured_at");
    const purchases = await items(api, p, p.purchase_type, "diesel_purchased_at");

    if (ctx.trigger?.kind === "manual") {
      for (const purchase of purchases) await price(api, purchase);
      let changed = 0;
      for (const reading of readings) if (await convert(api, p, reading, readings, purchases)) changed++;
      return { readings: readings.length, changed };
    }
    if (ctx.trigger?.kind !== "item") return undefined;

    const item = await api.v1.resources.byId(ctx.itemId!).get();
    if (!item) return undefined;
    if (ctx.trigger.item.content_type_key === "diesel_purchase") {
      await price(api, item);
      // The reading after the purchase is checked against it.
      const at = time(value(item, "diesel_purchased_at"));
      const next = readings.find((r) => time(value(r, "diesel_measured_at")) >= at);
      if (next) await convert(api, p, next, readings, purchases);
      return undefined;
    }
    // Only a reading whose values changed returns its levels, so its own
    // write-back does not ask the workflows that follow this one again.
    return convert(api, p, item, readings, purchases);
  },
});
