// Loads a year and a half of a real 750 L heating-oil tank into an empty
// Diesel list, for trying the tracker: four fills, nineteen dipstick
// readings and when the heating went on and off. Owners start it by hand,
// once; a list that has readings already is left as it is. The items are
// filed in time order, so the levels workflow checks each reading against
// the one before it.
import { json, workflow } from "@papergo/workflow";

interface Params {
  list: string;
  reading_type: string;
  purchase_type: string;
  heating_type: string;
}

// The stick is read from the filling mouth, 200 cm above the bottom.
const readings = [
  { at: "2025-03-04T11:00:00Z", stick: "76.0" },
  { at: "2025-03-22T10:00:00Z", stick: "94.0" },
  { at: "2025-04-12T10:00:00Z", stick: "103.0" },
  { at: "2025-05-11T10:00:00Z", stick: "112.0" },
  { at: "2025-07-25T10:00:00Z", stick: "127.0" },
  { at: "2025-09-02T10:00:00Z", stick: "130.0" },
  { at: "2025-10-29T10:00:00Z", stick: "142.0" },
  { at: "2025-11-08T10:00:00Z", stick: "153.0" },
  { at: "2025-11-14T09:00:00Z", stick: "159.6" },
  { at: "2025-11-14T13:00:00Z", stick: "76.2" },
  { at: "2025-11-24T10:00:00Z", stick: "86.8" },
  { at: "2026-01-02T10:00:00Z", stick: "133.5" },
  { at: "2026-01-26T13:00:00Z", stick: "75.0" },
  { at: "2026-03-14T10:00:00Z", stick: "109.0" },
  { at: "2026-04-18T10:00:00Z", stick: "124.0" },
  { at: "2026-05-21T10:00:00Z", stick: "132.0" },
  { at: "2026-06-23T10:00:00Z", stick: "145.0" },
  { at: "2026-08-02T10:00:00Z", stick: "151.5" },
  { at: "2026-09-03T13:00:00Z", stick: "75.0" },
];

const purchases = [
  { at: "2025-03-04T10:00:00Z", liters: "540.0", total: 587.52 },
  { at: "2025-11-14T12:00:00Z", liters: "500.0", total: 545.0 },
  { at: "2026-01-26T12:00:00Z", liters: "560.0", total: 661.36 },
  { at: "2026-09-03T12:00:00Z", liters: "535.0", total: 852.78 },
];

const heating = [
  { at: "2025-05-05T12:00:00Z", state: "off" },
  { at: "2025-10-29T12:00:00Z", state: "on" },
  { at: "2026-03-30T12:00:00Z", state: "off" },
];

export default workflow<unknown, unknown, Params>({
  triggers: [{ type: "manual", access: "manage" }],
  async run(ctx) {
    const p = ctx.params;
    const api = ctx.api;
    const existing = await api.v1.resources.byId(p.list).query.post({ query: { contentTypeId: p.reading_type as never } }, { queryParameters: { top: 1 } });
    if (existing?.value?.length) return "the list has readings already";

    const entries = [
      ...readings.map((r) => ({ at: r.at, name: `Reading ${r.at.slice(0, 10)} · ${r.stick} cm`, type: p.reading_type, values: { diesel_measured_at: r.at, diesel_stick_cm: r.stick } })),
      ...purchases.map((b) => ({ at: b.at, name: `Purchase ${b.at.slice(0, 10)} · ${b.liters} L`, type: p.purchase_type, values: { diesel_purchased_at: b.at, diesel_bought_liters: b.liters, diesel_total_price: b.total } })),
      ...heating.map((h) => ({ at: h.at, name: `Heating ${h.state} ${h.at.slice(0, 10)}`, type: p.heating_type, values: { diesel_heating_at: h.at, diesel_heating: h.state } })),
    ].sort((a, b) => a.at.localeCompare(b.at));
    for (const entry of entries) {
      await api.v1.resources.byId(p.list).children.post({ kind: "item", name: entry.name, contentTypeId: entry.type as never, values: json(entry.values) });
    }
    return { readings: readings.length, purchases: purchases.length, heating: heating.length };
  },
});
