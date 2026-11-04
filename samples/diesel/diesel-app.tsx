// The diesel tracker: how full the tank is now, what the diesel left is
// worth, how many days it lasts and when to order, from the dipstick
// readings, the purchases and when the heating went on and off. Readings
// are converted to litres by the Diesel levels workflow; the screen
// estimates today's level from the last reading at the season's rate of
// consumption, and forecasts forward the same way, assuming the heating
// goes on and off on its usual days. People add readings, purchases and
// heating changes here, or answer the measuring and refill tasks.
import { useEffect, useMemo, useState } from "react";
import {
  app,
  BarChart,
  Button,
  Card,
  DateTimeField,
  Gauge,
  Grid,
  LineChart,
  Notice,
  NumberField,
  Stat,
  Table,
  TextField,
  json,
  useApp,
  useQuery,
  type AppContext,
  type Band,
  type Item,
} from "@papergo/app";

interface Params {
  list: string;
  reading: string;
  purchase: string;
  heating: string;
  capacity_l: number;
  order_at_l: number;
  reserve_percent: number;
  rate_off: number;
  rate_on: number;
  rate_peak: number;
  peak_months: number[];
}

const DAY = 24 * 3600 * 1000;

const words = {
  en: {
    now: "Now", liters: "{n} L", estimated: "Estimated from {date}, {rate} L/day since", worth: "Worth {value} at {price}/L", reserve: "Days to the {p} % reserve", reserveOn: "On {date}", order: "Next order", orderAt: "When {l} L are left, in {days} days", daily: "Daily cost", dailyHint: "{rate} L/day, {season}",
    noReading: "No reading yet", noEstimate: "Not enough readings to estimate", heatingOn: "heating on", heatingOff: "heating off", peak: "heating on, winter", assumed: "assumed",
    level: "Level and heating", levelHint: "Red: heating on. Blue: heating off. Dashed lines: purchases.", forecast: "Forecast", percent: "Level %",
    price: "Price per litre", monthly: "Cost of the diesel used, per month", consumption: "Consumption", season: "Season", measured: "Measured", used: "Used in the forecast", days: "Days",
    off: "Heating off", on: "Heating on", winter: "Heating on, {months}",
    addReading: "Add a reading", stick: "Stick reading from the mouth", measuredAt: "Measured at", save: "Save", addPurchase: "Add a purchase", bought: "Litres bought", total: "Total price", boughtAt: "Bought at", supplier: "Supplier",
    heating: "Heating", heatingIs: "The heating is {state} since {date}.", heatingNow: "The heating is {state}.", stateOn: "on", stateOff: "off", turnOn: "It went on now", turnOff: "It went off now",
    measureDue: "A reading is due", refillDue: "Diesel is low: buy some", answer: "Answer", check: "Needs a look", checkHint: "Higher than the reading before and what was bought since. Open it to correct it, or mark it reviewed.",
    readings: "Readings", purchases: "Purchases", date: "Date", stickCol: "Stick", litersCol: "Litres", fill: "Fill", perLiter: "€/L", recalc: "Recalculate all", recalcStarted: "Recalculating every reading.", invalid: "Type a number", saved: "Saved", setUp: "Choose the Diesel list and its types in Workspace settings → Apps.",
  },
  es: {
    now: "Ahora", liters: "{n} L", estimated: "Estimado desde el {date}, a {rate} L/día", worth: "Vale {value} a {price}/L", reserve: "Días hasta la reserva del {p} %", reserveOn: "El {date}", order: "Próximo pedido", orderAt: "Cuando queden {l} L, en {days} días", daily: "Coste diario", dailyHint: "{rate} L/día, {season}",
    noReading: "Aún no hay mediciones", noEstimate: "Faltan mediciones para estimar", heatingOn: "calefacción encendida", heatingOff: "calefacción apagada", peak: "calefacción encendida, invierno", assumed: "supuesto",
    level: "Nivel y calefacción", levelHint: "Rojo: calefacción encendida. Azul: apagada. Líneas discontinuas: llenados.", forecast: "Previsión", percent: "Nivel %",
    price: "Precio por litro", monthly: "Coste del gasóleo consumido, por mes", consumption: "Consumo", season: "Temporada", measured: "Medido", used: "Usado en la previsión", days: "Días",
    off: "Calefacción apagada", on: "Calefacción encendida", winter: "Calefacción encendida, {months}",
    addReading: "Añadir una medición", stick: "Medida del palo desde la boca", measuredAt: "Medido el", save: "Guardar", addPurchase: "Añadir un llenado", bought: "Litros comprados", total: "Precio total", boughtAt: "Comprado el", supplier: "Proveedor",
    heating: "Calefacción", heatingIs: "La calefacción está {state} desde el {date}.", heatingNow: "La calefacción está {state}.", stateOn: "encendida", stateOff: "apagada", turnOn: "Se ha encendido ahora", turnOff: "Se ha apagado ahora",
    measureDue: "Toca medir", refillDue: "Queda poco gasóleo: hay que pedir", answer: "Responder", check: "Para revisar", checkHint: "Más alta que la medición anterior y lo comprado desde entonces. Ábrela para corregirla o márcala como revisada.",
    readings: "Mediciones", purchases: "Llenados", date: "Fecha", stickCol: "Palo", litersCol: "Litros", fill: "Nivel", perLiter: "€/L", recalc: "Recalcular todo", recalcStarted: "Recalculando todas las mediciones.", invalid: "Escribe un número", saved: "Guardado", setUp: "Elige la lista Diesel y sus tipos en Ajustes del espacio de trabajo → Aplicaciones.",
  },
};
type Words = typeof words.en;

function say(text: string, vars: Record<string, string | number> = {}): string {
  return text.replace(/\{(\w+)\}/g, (_, k: string) => String(vars[k] ?? ""));
}

const get = (item: Item, key: string): unknown => item.values?.[key];
const num = (v: unknown): number | undefined => (v === null || v === undefined || v === "" ? undefined : Number(v));
const at = (v: unknown): number => new Date(String(v)).getTime();

interface Reading { id: string; at: number; stick?: number; liters?: number; percent?: number; flag?: string }
interface Purchase { id: string; at: number; liters: number; total: number; price: number }
interface Switch { at: number; on: boolean }

const dayOfYear = (t: number) => {
  const d = new Date(t);
  return Math.floor((Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), d.getUTCDate()) - Date.UTC(d.getUTCFullYear(), 0, 1)) / DAY);
};
const average = (xs: number[]) => (xs.length ? xs.reduce((a, b) => a + b, 0) / xs.length : undefined);

/**
 * When the heating is on: as recorded, and after the last recorded change
 * as usual, switching on and off on the average days of the year of the
 * recorded changes.
 */
function heatingModel(switches: Switch[]) {
  const usualOn = Math.round(average(switches.filter((s) => s.on).map((s) => dayOfYear(s.at))) ?? 298);
  const usualOff = Math.round(average(switches.filter((s) => !s.on).map((s) => dayOfYear(s.at))) ?? 105);
  const nextDay = (doy: number, after: number) => {
    const year = new Date(after).getUTCFullYear();
    for (const y of [year, year + 1, year + 2]) {
      const t = Date.UTC(y, 0, 1) + doy * DAY + DAY / 2;
      if (t > after) return t;
    }
    return Infinity;
  };
  return (t: number): { on: boolean; assumed: boolean; since?: number } => {
    const recorded = switches.filter((s) => s.at <= t);
    const last = recorded.at(-1);
    if (!last) return { on: switches[0] ? !switches[0].on : false, assumed: true };
    if (last !== switches.at(-1)) return { on: last.on, assumed: false, since: last.at };
    let on = last.on;
    let cursor = last.at;
    let assumed = false;
    for (;;) {
      const next = nextDay(on ? usualOff : usualOn, cursor);
      if (next > t) return { on, assumed, since: cursor };
      on = !on;
      cursor = next;
      assumed = true;
    }
  };
}

function useDiesel(ctx: AppContext<Params>) {
  const p = ctx.params;
  const readingItems = useQuery(p.list, { contentTypeId: p.reading, sort: [{ field: "diesel_measured_at", direction: "asc" }] }, { all: true, live: true });
  const purchaseItems = useQuery(p.list, { contentTypeId: p.purchase, sort: [{ field: "diesel_purchased_at", direction: "asc" }] }, { all: true, live: true });
  const heatingItems = useQuery(p.list, { contentTypeId: p.heating, sort: [{ field: "diesel_heating_at", direction: "asc" }] }, { all: true, live: true });
  const model = useMemo(() => {
    const readings: Reading[] = readingItems.data.map((i) => ({ id: i.id, at: at(get(i, "diesel_measured_at")), stick: num(get(i, "diesel_stick_cm")), liters: num(get(i, "diesel_level_liters")), percent: num(get(i, "diesel_fill_percent")), flag: get(i, "diesel_flag") as string | undefined }));
    const purchases: Purchase[] = purchaseItems.data.map((i) => {
      const liters = num(get(i, "diesel_bought_liters")) ?? 0;
      const total = num(get(i, "diesel_total_price")) ?? 0;
      return { id: i.id, at: at(get(i, "diesel_purchased_at")), liters, total, price: num(get(i, "diesel_price_per_liter")) ?? (liters ? total / liters : 0) };
    });
    const switches: Switch[] = heatingItems.data.map((i) => ({ at: at(get(i, "diesel_heating_at")), on: get(i, "diesel_heating") === "on" }));
    const heating = heatingModel(switches);
    const peak = new Set(p.peak_months ?? [12, 1, 2]);
    const season = (t: number): "off" | "on" | "peak" => (!heating(t).on ? "off" : peak.has(new Date(t).getUTCMonth() + 1) ? "peak" : "on");
    const rate = (t: number) => ({ off: p.rate_off, on: p.rate_on, peak: p.rate_peak })[season(t)];
    const priceAt = (t: number) => (purchases.filter((b) => b.at <= t).at(-1) ?? purchases[0])?.price ?? 0;
    const bought = (from: number, to: number) => purchases.filter((b) => b.at > from && b.at <= to).reduce((sum, b) => sum + b.liters, 0);

    // Each day's consumption between converted readings, and what it cost.
    const converted = readings.filter((r) => r.liters !== undefined);
    const used = new Map<string, { liters: number; cost: number }>();
    const measured = { off: { liters: 0, days: 0 }, on: { liters: 0, days: 0 }, peak: { liters: 0, days: 0 } };
    for (let i = 1; i < converted.length; i++) {
      const a = converted[i - 1];
      const b = converted[i];
      const days = (b.at - a.at) / DAY;
      const consumed = a.liters! + bought(a.at, b.at) - b.liters!;
      if (days < 0.5 || consumed < -5) continue;
      for (let t = a.at; t < b.at; t += DAY) {
        const share = (Math.min(DAY, b.at - t) / DAY) * (Math.max(consumed, 0) / days);
        const month = new Date(t).toISOString().slice(0, 7);
        const entry = used.get(month) ?? { liters: 0, cost: 0 };
        entry.liters += share;
        entry.cost += share * priceAt(t);
        used.set(month, entry);
        const s = season(t);
        measured[s].liters += share;
        measured[s].days += Math.min(DAY, b.at - t) / DAY;
      }
    }

    // Today's level: the last reading less the season's consumption since,
    // plus what was bought after it.
    const now = Date.now();
    const last = converted.at(-1);
    let current: number | undefined;
    if (last) {
      current = last.liters!;
      for (let t = last.at; t < now; t += DAY) current -= rate(t) * (Math.min(DAY, now - t) / DAY);
      current = Math.min(Math.max(current + bought(last.at, now), 0), p.capacity_l);
    }
    const until = (target: number) => {
      if (current === undefined || current <= target) return current === undefined ? undefined : 0;
      let left = current;
      for (let day = 0; day < 3 * 365; day++) {
        left -= rate(now + day * DAY);
        if (left <= target) return day + 1;
      }
      return undefined;
    };
    const price = purchases.at(-1)?.price ?? 0;

    // Heating bands over the readings' time, a day at a time.
    const bands: Band[] = [];
    const start = readings[0]?.at;
    if (start !== undefined) {
      for (let t = start; t <= now; t += DAY) {
        const on = heating(t).on;
        const lastBand = bands.at(-1);
        if (lastBand && (lastBand.tone === "warm") === on) lastBand.to = t + DAY;
        else bands.push({ from: t, to: t + DAY, tone: on ? "warm" : "cool" });
      }
    }
    return { readings, purchases, switches, heating, season, rate, current, until, price, used, measured, bands, last, now };
  }, [readingItems.data, purchaseItems.data, heatingItems.data, p]);
  return { model, loading: readingItems.loading || purchaseItems.loading || heatingItems.loading, error: readingItems.error ?? purchaseItems.error ?? heatingItems.error };
}

interface Task { id: string; title: string }

function useTasks(ctx: AppContext<Params>, key: string): Task[] {
  const [tasks, setTasks] = useState<Task[]>([]);
  useEffect(() => {
    let stale = false;
    ctx.request<{ value: Task[] }>("GET", `/v1/me/tasks?key=${key}&status=open`).then((r) => !stale && setTasks(r.data.value ?? []), () => !stale && setTasks([]));
    return () => {
      stale = true;
    };
  }, [ctx, key]);
  return tasks;
}

function Dashboard({ w }: { w: Words }) {
  const ctx = useApp<Params>();
  const p = ctx.params;
  const f = ctx.format;
  const { model: m, error } = useDiesel(ctx);
  const measure = useTasks(ctx, "diesel_measure");
  const refill = useTasks(ctx, "diesel_refill");
  const [notice, setNotice] = useState<string>();
  const seasonName = (s: "off" | "on" | "peak") => ({ off: w.heatingOff, on: w.heatingOn, peak: w.peak })[s];
  const reserveLiters = (p.reserve_percent / 100) * p.capacity_l;
  const toReserve = m.until(reserveLiters);
  const toOrder = m.until(p.order_at_l);
  const date = (days?: number) => (days === undefined ? "–" : f.date(m.now + days * DAY));
  const euro = (v: number) => f.currency(v, "EUR");
  const nowSeason = m.season(m.now);
  const heatingNow = m.heating(m.now);
  const flagged = m.readings.filter((r) => r.flag === "check");
  const months = [...m.used.entries()].sort(([a], [b]) => a.localeCompare(b)).slice(-12);
  const monthName = (month: string) => new Date(`${month}-15T00:00:00Z`).toLocaleDateString(ctx.user.preferences.language, { month: "short", year: "2-digit", timeZone: "UTC" });

  const recalc = async () => {
    const list = await ctx.request<{ value: { id: string; key: string }[] }>("GET", `/v1/workspaces/${ctx.workspace.id}/workflows`);
    const levels = list.data.value.find((wf) => wf.key === "diesel-levels");
    if (!levels) return;
    await ctx.request("POST", `/v1/workflows/${levels.id}/runs`, { body: {} });
    setNotice(w.recalcStarted);
  };

  if (error) return <Notice tone="danger" title={error.message}>{w.setUp}</Notice>;
  return (
    <>
      {measure.map((task) => <Notice key={task.id} tone="warning" title={w.measureDue} action={{ label: w.answer, onClick: () => ctx.open({ taskId: task.id }) }}>{task.title}</Notice>)}
      {refill.map((task) => <Notice key={task.id} tone="danger" title={w.refillDue} action={{ label: w.answer, onClick: () => ctx.open({ taskId: task.id }) }}>{task.title}</Notice>)}
      {notice && <Notice tone="success">{notice}</Notice>}

      <Grid min={200}>
        {m.current === undefined ? (
          <Stat label={w.now} value={w.noReading} />
        ) : (
          <Gauge label={w.now} value={m.current} max={p.capacity_l} warn={p.order_at_l / p.capacity_l} danger={p.reserve_percent / 100} hint={say(w.estimated, { date: f.date(m.last!.at), rate: f.number(m.rate(m.now), 1) })} />
        )}
        <Stat label={w.litersCol} value={m.current === undefined ? "–" : say(w.liters, { n: f.number(m.current, 0) })} hint={m.current === undefined ? undefined : say(w.worth, { value: euro(m.current * m.price), price: f.decimal(m.price, 3) })} />
        <Stat label={say(w.reserve, { p: p.reserve_percent })} value={toReserve ?? "–"} hint={toReserve === undefined ? w.noEstimate : say(w.reserveOn, { date: date(toReserve) })} tone={toReserve !== undefined && toReserve < 30 ? "warning" : "default"} />
        <Stat label={w.order} value={date(toOrder)} hint={toOrder === undefined ? w.noEstimate : say(w.orderAt, { l: p.order_at_l, days: toOrder })} tone={toOrder !== undefined && toOrder < 14 ? "danger" : "default"} />
        <Stat label={w.daily} value={euro(m.rate(m.now) * m.price)} hint={say(w.dailyHint, { rate: f.number(m.rate(m.now), 1), season: seasonName(nowSeason) + (heatingNow.assumed ? ` (${w.assumed})` : "") })} />
      </Grid>

      <Card title={w.level}>
        <LineChart
          height={240}
          yMin={0}
          yMax={100}
          bands={m.bands}
          markers={m.purchases.map((b) => ({ x: b.at, label: `${f.date(b.at)} · ${f.number(b.liters, 0)} L · ${euro(b.total)}` }))}
          series={[
            { name: w.percent, points: m.readings.filter((r) => r.percent !== undefined).map((r) => ({ x: r.at, y: r.percent! })) },
            ...(m.current !== undefined && m.last ? [{ name: w.forecast, points: [{ x: m.last.at, y: m.last.percent ?? (m.last.liters! / p.capacity_l) * 100 }, { x: m.now, y: (m.current / p.capacity_l) * 100 }, ...(toOrder ? [{ x: m.now + toOrder * DAY, y: (p.order_at_l / p.capacity_l) * 100 }] : [])] }] : []),
          ]}
          formatX={(x) => f.date(x)}
          formatY={(y) => `${Math.round(y)} %`}
        />
        <p className="pg-stat-hint">{w.levelHint}</p>
      </Card>

      <Grid min={300}>
        <Card title={w.price}>
          <LineChart height={200} series={[{ name: w.perLiter, points: m.purchases.map((b) => ({ x: b.at, y: b.price })) }]} formatX={(x) => f.date(x)} formatY={(y) => f.decimal(y, 2)} />
        </Card>
        <Card title={w.monthly}>
          <BarChart height={200} data={months.map(([month, u]) => ({ label: monthName(month), value: u.cost }))} formatY={(y) => `${f.number(y, 0)} €`} />
        </Card>
      </Grid>

      <Card title={w.consumption}>
        <Table
          columns={[
            { key: "season", label: w.season },
            { key: "measured", label: w.measured, align: "right" },
            { key: "used", label: w.used, align: "right" },
            { key: "days", label: w.days, align: "right" },
          ]}
          rows={(["off", "on", "peak"] as const).map((s) => ({
            id: s,
            season: s === "off" ? w.off : s === "on" ? w.on : say(w.winter, { months: (p.peak_months ?? []).join(", ") }),
            measured: m.measured[s].days ? `${f.number(m.measured[s].liters / m.measured[s].days, 1)} L/d` : "–",
            used: `${f.number({ off: p.rate_off, on: p.rate_on, peak: p.rate_peak }[s], 1)} L/d`,
            days: f.number(m.measured[s].days, 0),
          }))}
        />
      </Card>

      {flagged.length > 0 && (
        <Card title={w.check}>
          <p className="pg-stat-hint">{w.checkHint}</p>
          <Table columns={[{ key: "date", label: w.date }, { key: "stick", label: w.stickCol, align: "right" }, { key: "liters", label: w.litersCol, align: "right" }]} rows={flagged.map((r) => ({ id: r.id, date: f.dateTime(r.at), stick: `${f.number(r.stick, 1)} cm`, liters: f.number(r.liters, 1) }))} onRowClick={(row) => ctx.open({ itemId: String(row.id) })} />
        </Card>
      )}

      <Grid min={260}>
        <AddReading w={w} />
        <AddPurchase w={w} />
        <Card title={w.heating}>
          <p>{heatingNow.since && !heatingNow.assumed ? say(w.heatingIs, { state: heatingNow.on ? w.stateOn : w.stateOff, date: f.date(heatingNow.since) }) : say(w.heatingNow, { state: (heatingNow.on ? w.stateOn : w.stateOff) + ` (${w.assumed})` })}</p>
          <Button variant="primary" onClick={() => void addHeating(ctx, !heatingNow.on)}>{heatingNow.on ? w.turnOff : w.turnOn}</Button>
          {ctx.workspace.role === "owner" && (
            <p style={{ marginTop: 12 }}>
              <Button onClick={() => void recalc()}>{w.recalc}</Button>
            </p>
          )}
        </Card>
      </Grid>

      <Grid min={320}>
        <Card title={w.readings}>
          <Table columns={[{ key: "date", label: w.date }, { key: "stick", label: w.stickCol, align: "right" }, { key: "liters", label: w.litersCol, align: "right" }, { key: "fill", label: w.fill, align: "right" }]} rows={[...m.readings].reverse().slice(0, 12).map((r) => ({ id: r.id, date: f.date(r.at), stick: `${f.number(r.stick, 1)} cm`, liters: f.number(r.liters, 1), fill: r.percent === undefined ? "…" : `${f.number(r.percent, 1)} %` }))} onRowClick={(row) => ctx.open({ itemId: String(row.id) })} />
        </Card>
        <Card title={w.purchases}>
          <Table columns={[{ key: "date", label: w.date }, { key: "liters", label: w.litersCol, align: "right" }, { key: "total", label: w.total, align: "right" }, { key: "price", label: w.perLiter, align: "right" }]} rows={[...m.purchases].reverse().map((b) => ({ id: b.id, date: f.date(b.at), liters: f.number(b.liters, 0), total: euro(b.total), price: f.decimal(b.price, 3) }))} onRowClick={(row) => ctx.open({ itemId: String(row.id) })} />
        </Card>
      </Grid>
    </>
  );
}

function addHeating(ctx: AppContext<Params>, on: boolean) {
  const now = new Date().toISOString();
  return ctx.api.v1.resources.byId(ctx.params.list).children.post({ kind: "item", name: `Heating ${on ? "on" : "off"} ${now.slice(0, 10)}`, contentTypeId: ctx.params.heating as never, values: json({ diesel_heating_at: now, diesel_heating: on ? "on" : "off" }) });
}

function AddReading({ w }: { w: Words }) {
  const ctx = useApp<Params>();
  const [stick, setStick] = useState("");
  const [when, setWhen] = useState(() => new Date().toISOString());
  const [state, setState] = useState<"idle" | "saving" | "saved">("idle");
  const value = ctx.format.parseDecimal(stick);
  const save = async () => {
    if (value === undefined) return;
    setState("saving");
    await ctx.api.v1.resources.byId(ctx.params.list).children.post({ kind: "item", name: `Reading ${when.slice(0, 10)} · ${value} cm`, contentTypeId: ctx.params.reading as never, values: json({ diesel_measured_at: when, diesel_stick_cm: value }) });
    setStick("");
    setState("saved");
  };
  return (
    <Card title={w.addReading}>
      <NumberField label={w.stick} unit="cm" value={stick} onChange={(v) => { setStick(v); setState("idle"); }} error={stick && value === undefined ? w.invalid : undefined} />
      <DateTimeField label={w.measuredAt} value={when} onChange={setWhen} />
      <Button variant="primary" disabled={value === undefined || state === "saving"} onClick={() => void save()}>{state === "saved" ? w.saved : w.save}</Button>
    </Card>
  );
}

function AddPurchase({ w }: { w: Words }) {
  const ctx = useApp<Params>();
  const [liters, setLiters] = useState("");
  const [total, setTotal] = useState("");
  const [supplier, setSupplier] = useState("");
  const [when, setWhen] = useState(() => new Date().toISOString());
  const [state, setState] = useState<"idle" | "saving" | "saved">("idle");
  const l = ctx.format.parseDecimal(liters);
  const t = ctx.format.parseDecimal(total);
  const save = async () => {
    if (l === undefined || t === undefined) return;
    setState("saving");
    await ctx.api.v1.resources.byId(ctx.params.list).children.post({
      kind: "item",
      name: `Purchase ${when.slice(0, 10)} · ${l} L`,
      contentTypeId: ctx.params.purchase as never,
      values: json({ diesel_purchased_at: when, diesel_bought_liters: l, diesel_total_price: Number(t), ...(supplier ? { diesel_supplier: supplier } : {}) }),
    });
    setLiters("");
    setTotal("");
    setSupplier("");
    setState("saved");
  };
  return (
    <Card title={w.addPurchase}>
      <NumberField label={w.bought} unit="L" value={liters} onChange={(v) => { setLiters(v); setState("idle"); }} error={liters && l === undefined ? w.invalid : undefined} />
      <NumberField label={w.total} unit="€" value={total} onChange={setTotal} error={total && t === undefined ? w.invalid : undefined} />
      <DateTimeField label={w.boughtAt} value={when} onChange={setWhen} />
      <TextField label={w.supplier} value={supplier} onChange={setSupplier} maxLength={200} />
      <Button variant="primary" disabled={l === undefined || t === undefined || state === "saving"} onClick={() => void save()}>{state === "saved" ? w.saved : w.save}</Button>
    </Card>
  );
}

export default app<Params>({
  parameters: {
    type: "object",
    required: ["list", "reading", "purchase", "heating"],
    properties: {
      list: { type: "string", title: "Diesel list", "x-papergo": { kind: "collection" } },
      reading: { type: "string", title: "Reading type", "x-papergo": { kind: "content_type" } },
      purchase: { type: "string", title: "Purchase type", "x-papergo": { kind: "content_type" } },
      heating: { type: "string", title: "Heating type", "x-papergo": { kind: "content_type" } },
      capacity_l: { type: "number", title: "Tank capacity (L)", default: 750, minimum: 1 },
      order_at_l: { type: "number", title: "Order when this much is left (L)", default: 200, minimum: 0 },
      reserve_percent: { type: "number", title: "Reserve (%)", default: 25, minimum: 0, maximum: 100 },
      rate_off: { type: "number", title: "Litres a day with the heating off", default: 1.5, minimum: 0 },
      rate_on: { type: "number", title: "Litres a day with the heating on", default: 4.5, minimum: 0 },
      rate_peak: { type: "number", title: "Litres a day with the heating on in the coldest months", default: 7.5, minimum: 0 },
      peak_months: { type: "array", title: "Coldest months", items: { type: "integer", minimum: 1, maximum: 12 }, default: [12, 1, 2] },
    },
  },
  render(ctx) {
    const w = ctx.user.preferences.language.startsWith("es") ? words.es : words.en;
    return <Dashboard w={w} />;
  },
});
