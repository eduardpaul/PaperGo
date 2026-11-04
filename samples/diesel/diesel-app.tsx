// The diesel tracker: how full the tank is now, what the diesel left is
// worth, how many days it lasts and when to order, from the dipstick
// readings, the purchases and when the heating went on and off. Readings
// are converted to litres by the Diesel levels workflow, whose settings
// also give the preview of a reading before it is saved; the screen
// estimates today's level from the last reading at the season's rate of
// consumption, and forecasts forward the same way, assuming the heating
// goes on and off on its usual days. People add readings, purchases and
// heating changes from the buttons at the top, or answer the measuring and
// refill tasks.
import { useEffect, useMemo, useState } from "react";
import {
  app,
  BarChart,
  Button,
  Card,
  DateField,
  Gauge,
  Grid,
  Legend,
  LineChart,
  Modal,
  Notice,
  NumberField,
  Table,
  TextField,
  Toolbar,
  json,
  useApp,
  useNarrow,
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
    tank: "Tank", autonomy: "Autonomy and forecast", pace: "Pace of consumption",
    liters: "{n} L", worth: "Worth {value} at {price}/L", estimated: "Estimated from the reading of {date}", noReading: "No reading yet", noEstimate: "Not enough readings to forecast",
    orderIn: "Order in {days} days, when {l} L are left", reserveOn: "{p} % reserve on {date}, in {days} days", perDay: "{cost}/day", paceHint: "{rate} L/day · {season}", extremes: "Deep winter {peak}/day · summer {summer}/day",
    off: "Hot water only (heating off)", on: "Spring and autumn (heating on)", winter: "Deep winter ({months})", assumed: "assumed",
    level: "Level and heating", heatingOnBand: "Heating on", heatingOffBand: "Heating off", levelLine: "Level", forecastLine: "Forecast", fillLine: "Fill", forecast: "Forecast", percent: "Level %", reserveLine: "Reserve {p} %",
    price: "Price per litre", monthly: "Cost of the diesel used, per month", consumption: "Consumption", season: "Season", measured: "Measured", used: "Used in the forecast", days: "Days",
    addReading: "+ Reading", addPurchase: "+ Fill", heatingButton: "Heating: {state}", stateOn: "on", stateOff: "off",
    readingTitle: "New reading", stick: "Stick reading from the mouth", measuredAt: "Date", preview: "That is about {p} % ({l} L)", outOfRange: "The stick is {max} cm long",
    purchaseTitle: "New fill", bought: "Litres bought", total: "Total price", boughtAt: "Date", supplier: "Supplier", pricePreview: "{price}/L",
    heatingTitle: "Heating", heatingIs: "The heating is {state} since {date}.", heatingNow: "The heating is {state} ({assumed}).", changedAt: "Since", turnOn: "It went on", turnOff: "It went off",
    save: "Save", cancel: "Cancel", invalid: "Type a number",
    measureDue: "A reading is due", refillDue: "Diesel is low: buy some", answer: "Answer", check: "Needs a look", checkHint: "Higher than the reading before and what was bought since. Open it to correct it, or mark it reviewed.",
    readings: "Readings", purchases: "Fills", date: "Date", stickCol: "Stick", litersCol: "Litres", fill: "Level", perLiter: "€/L", supplierCol: "Supplier", history: "See the whole history", setUp: "Choose the Diesel list and its types in Workspace settings → Apps.",
  },
  es: {
    tank: "Estado del tanque", autonomy: "Autonomía y previsión", pace: "Ritmo de consumo",
    liters: "{n} L", worth: "Vale {value} a {price}/L", estimated: "Estimado desde la medición del {date}", noReading: "Aún no hay mediciones", noEstimate: "Faltan mediciones para prever",
    orderIn: "Pedir en {days} días, cuando queden {l} L", reserveOn: "Reserva del {p} % el {date}, en {days} días", perDay: "{cost}/día", paceHint: "{rate} L/día · {season}", extremes: "Pleno invierno {peak}/día · verano {summer}/día",
    off: "Solo agua caliente (calefacción apagada)", on: "Entretiempo (otoño / primavera)", winter: "Pleno invierno ({months})", assumed: "supuesto",
    level: "Nivel y calefacción", heatingOnBand: "Calefacción encendida", heatingOffBand: "Calefacción apagada", levelLine: "Nivel", forecastLine: "Previsión", fillLine: "Llenado", forecast: "Previsión", percent: "Nivel %", reserveLine: "Reserva {p} %",
    price: "Precio por litro", monthly: "Coste del gasóleo consumido, por mes", consumption: "Consumo", season: "Temporada", measured: "Medido", used: "Usado en la previsión", days: "Días",
    addReading: "+ Medición", addPurchase: "+ Llenado", heatingButton: "Calefacción: {state}", stateOn: "encendida", stateOff: "apagada",
    readingTitle: "Nueva medición", stick: "Medida del palo desde la boca", measuredAt: "Fecha", preview: "Equivale a ~{p} % ({l} L)", outOfRange: "El palo mide {max} cm",
    purchaseTitle: "Nuevo llenado", bought: "Litros comprados", total: "Precio total", boughtAt: "Fecha", supplier: "Proveedor", pricePreview: "{price}/L",
    heatingTitle: "Calefacción", heatingIs: "La calefacción está {state} desde el {date}.", heatingNow: "La calefacción está {state} ({assumed}).", changedAt: "Desde", turnOn: "Se ha encendido", turnOff: "Se ha apagado",
    save: "Guardar", cancel: "Cancelar", invalid: "Escribe un número",
    measureDue: "Toca medir", refillDue: "Queda poco gasóleo: hay que pedir", answer: "Responder", check: "Para revisar", checkHint: "Más alta que la medición anterior y lo comprado desde entonces. Ábrela para corregirla o márcala como revisada.",
    readings: "Mediciones", purchases: "Llenados", date: "Fecha", stickCol: "Palo", litersCol: "Litros", fill: "Nivel", perLiter: "€/L", supplierCol: "Proveedor", history: "Ver historial completo", setUp: "Elige la lista Diesel y sus tipos en Ajustes del espacio de trabajo → Aplicaciones.",
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
interface Purchase { id: string; at: number; liters: number; total: number; price: number; supplier: string }
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
      return { id: i.id, at: at(get(i, "diesel_purchased_at")), liters, total, price: num(get(i, "diesel_price_per_liter")) ?? (liters ? total / liters : 0), supplier: String(get(i, "diesel_supplier") ?? "") };
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

/** The viewer's open tasks with key, read again when changed changes: a reading or a fill closes them. */
function useTasks(ctx: AppContext<Params>, key: string, changed: unknown): Task[] {
  const [tasks, setTasks] = useState<Task[]>([]);
  useEffect(() => {
    let stale = false;
    ctx.request<{ value: Task[] }>("GET", `/v1/me/tasks?key=${encodeURIComponent(key)}&status=open`).then((r) => !stale && setTasks(r.data.value ?? []), () => !stale && setTasks([]));
    return () => {
      stale = true;
    };
  }, [ctx, key, changed]);
  return tasks;
}

/** A dialog's date, now each time it opens, and its save, which closes it or says why it failed. */
function useEntry(open: boolean, onClose: () => void) {
  const [when, setWhen] = useState(() => new Date().toISOString());
  const [saving, setSaving] = useState(false);
  const [failed, setFailed] = useState<string>();
  useEffect(() => {
    if (!open) return;
    setWhen(new Date().toISOString());
    setFailed(undefined);
  }, [open]);
  const save = async (write: () => Promise<unknown>) => {
    setSaving(true);
    setFailed(undefined);
    try {
      await write();
      onClose();
    } catch (e) {
      setFailed(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  };
  return { when, setWhen, saving, failed, save };
}

/** How the Diesel levels workflow reads the stick, from its settings. */
interface Geometry {
  capacity_l: number;
  useful_height_cm: number;
  stick_length_cm: number;
  measure: "from_top" | "depth";
  orientation: "upright" | "horizontal";
}

function useGeometry(ctx: AppContext<Params>): Geometry | undefined {
  const [geometry, setGeometry] = useState<Geometry>();
  useEffect(() => {
    ctx.request<{ value: { key: string; parameters?: Geometry }[] }>("GET", `/v1/workspaces/${ctx.workspace.id}/workflows`).then(
      (r) => setGeometry(r.data.value.find((w) => w.key === "diesel-levels")?.parameters),
      () => undefined,
    );
  }, [ctx]);
  return geometry;
}

/** The litres of a stick reading, as the Diesel levels workflow works them out. */
function litersOf(g: Geometry, stick: number): number {
  const wet = Math.min(Math.max(g.measure === "depth" ? stick : g.stick_length_cm - stick, 0), g.useful_height_cm);
  if (g.orientation === "horizontal") {
    const theta = 2 * Math.acos(1 - (2 * wet) / g.useful_height_cm);
    return (g.capacity_l * (theta - Math.sin(theta))) / (2 * Math.PI);
  }
  return (wet / g.useful_height_cm) * g.capacity_l;
}

const big = { fontSize: 26, fontWeight: 650, fontVariantNumeric: "tabular-nums" } as const;

function Dashboard({ w }: { w: Words }) {
  const ctx = useApp<Params>();
  const p = ctx.params;
  const f = ctx.format;
  const lang = ctx.user.preferences.language;
  const { model: m, error } = useDiesel(ctx);
  const geometry = useGeometry(ctx);
  const measure = useTasks(ctx, "diesel_measure", m);
  const refill = useTasks(ctx, "diesel_refill", m);
  const [dialog, setDialog] = useState<"reading" | "purchase" | "heating">();
  const narrow = useNarrow();
  const reserveLiters = (p.reserve_percent / 100) * p.capacity_l;
  const toReserve = m.until(reserveLiters);
  const toOrder = m.until(p.order_at_l);
  const date = (days: number) => f.date(m.now + days * DAY);
  const euro = (v: number) => f.currency(v, "EUR");
  const monthName = (t: number) => new Date(t).toLocaleDateString(lang, { month: "short", year: "2-digit", timeZone: "UTC" });
  const peakNames = (p.peak_months ?? []).map((n) => new Date(Date.UTC(2000, n - 1, 15)).toLocaleDateString(lang, { month: "short", timeZone: "UTC" }));
  const seasonName = (s: "off" | "on" | "peak") => (s === "off" ? w.off : s === "on" ? w.on : say(w.winter, { months: peakNames.length > 1 ? `${peakNames[0]} – ${peakNames.at(-1)}` : peakNames.join("") }));
  const nowSeason = m.season(m.now);
  const heatingNow = m.heating(m.now);
  const flagged = m.readings.filter((r) => r.flag === "check");
  const months = [...m.used.entries()].sort(([a], [b]) => a.localeCompare(b)).slice(-12);
  // A tick at the start of every third month of the chart.
  const ticks = (from: number, to: number) => {
    const out: number[] = [];
    const d = new Date(from);
    for (let t = Date.UTC(d.getUTCFullYear(), d.getUTCMonth() + 1, 1); t <= to; ) {
      out.push(t);
      const n = new Date(t);
      t = Date.UTC(n.getUTCFullYear(), n.getUTCMonth() + 3, 1);
    }
    return out;
  };
  const levelEnd = toOrder ? m.now + toOrder * DAY : m.now;

  if (error) return <Notice tone="danger" title={error.message}>{w.setUp}</Notice>;
  return (
    <>
      <Toolbar>
        <Button variant="primary" onClick={() => setDialog("reading")}>{w.addReading}</Button>
        <Button onClick={() => setDialog("purchase")}>{w.addPurchase}</Button>
        <Button onClick={() => setDialog("heating")}>
          {narrow
            ? `🔥 ${heatingNow.on ? w.stateOn : w.stateOff}`
            : "🔥 " + say(w.heatingButton, { state: heatingNow.on ? w.stateOn : w.stateOff }) + (heatingNow.assumed ? ` (${w.assumed})` : "")}
        </Button>
      </Toolbar>
      {measure.map((task) => <Notice key={task.id} tone="warning" title={w.measureDue} action={{ label: w.answer, onClick: () => ctx.open({ taskId: task.id }) }}>{task.title}</Notice>)}
      {refill.map((task) => <Notice key={task.id} tone="danger" title={w.refillDue} action={{ label: w.answer, onClick: () => ctx.open({ taskId: task.id }) }}>{task.title}</Notice>)}

      <Grid min={280}>
        <Card title={w.tank}>
          {m.current === undefined ? (
            <p>{w.noReading}</p>
          ) : (
            <div style={{ display: "grid", gridTemplateColumns: "minmax(110px, 1fr) 1.2fr", gap: 12, alignItems: "center" }}>
              <Gauge bare value={m.current} max={p.capacity_l} warn={p.order_at_l / p.capacity_l} danger={p.reserve_percent / 100} />
              <div>
                <div style={big}>{say(w.liters, { n: f.number(m.current, 0) })}</div>
                <div className="pg-stat-hint">{say(w.worth, { value: euro(m.current * m.price), price: euro(m.price) })}</div>
                <div className="pg-stat-hint">{say(w.estimated, { date: f.date(m.last!.at) })}</div>
              </div>
            </div>
          )}
        </Card>
        <Card title={w.autonomy}>
          {toOrder === undefined ? (
            <p>{w.noEstimate}</p>
          ) : (
            <>
              <div style={big}>{date(toOrder)}</div>
              <div className="pg-stat-hint">{say(w.orderIn, { days: toOrder, l: p.order_at_l })}</div>
              {toReserve !== undefined && <div className="pg-stat-hint">{say(w.reserveOn, { p: p.reserve_percent, date: date(toReserve), days: toReserve })}</div>}
            </>
          )}
        </Card>
        <Card title={w.pace}>
          <div style={big}>{say(w.perDay, { cost: euro(m.rate(m.now) * m.price) })}</div>
          <div className="pg-stat-hint">{say(w.paceHint, { rate: f.number(m.rate(m.now), 1), season: seasonName(nowSeason) + (heatingNow.assumed ? ` (${w.assumed})` : "") })}</div>
          <div className="pg-stat-hint">{say(w.extremes, { peak: euro(p.rate_peak * m.price), summer: euro(p.rate_off * m.price) })}</div>
        </Card>
      </Grid>

      <Card
        title={w.level}
        actions={<Legend items={[{ label: w.heatingOnBand, color: "rgba(220, 38, 38, .35)" }, { label: w.heatingOffBand, color: "rgba(37, 99, 235, .25)" }, { label: w.levelLine, color: "var(--pg-series-1)" }, { label: w.forecastLine, color: "var(--pg-series-2)" }]} />}
      >
        <LineChart
          height={260}
          yMin={0}
          yMax={100}
          bands={m.bands}
          references={[{ y: p.reserve_percent, label: say(w.reserveLine, { p: p.reserve_percent }) }]}
          xTicks={m.readings[0] ? ticks(m.readings[0].at, levelEnd) : undefined}
          markers={m.purchases.map((b) => ({ x: b.at, label: `${w.fillLine} ${f.date(b.at)} · ${f.number(b.liters, 0)} L · ${euro(b.total)}` }))}
          series={[
            { name: w.levelLine, points: m.readings.filter((r) => r.percent !== undefined).map((r) => ({ x: r.at, y: r.percent! })) },
            ...(m.current !== undefined && m.last ? [{ name: w.forecast, points: [{ x: m.last.at, y: m.last.percent ?? (m.last.liters! / p.capacity_l) * 100 }, { x: m.now, y: (m.current / p.capacity_l) * 100 }, ...(toOrder ? [{ x: levelEnd, y: (p.order_at_l / p.capacity_l) * 100 }] : [])] }] : []),
          ]}
          formatX={(x) => f.date(x)}
          formatTick={monthName}
          formatY={(y) => `${Math.round(y)} %`}
        />
      </Card>

      <Grid min={300}>
        <Card title={w.price}>
          {m.purchases.length > 0 && (
            <LineChart
              height={200}
              yMin={Math.max(0, Math.min(...m.purchases.map((b) => b.price)) - 0.1)}
              yMax={Math.max(...m.purchases.map((b) => b.price)) + 0.1}
              xTicks={m.purchases.map((b) => b.at)}
              series={[{ name: w.perLiter, points: m.purchases.map((b) => ({ x: b.at, y: b.price })) }]}
              formatX={(x) => f.date(x)}
              formatTick={monthName}
              formatY={(y) => euro(y)}
            />
          )}
        </Card>
        <Card title={w.monthly}>
          <BarChart height={200} data={months.map(([month, u]) => ({ label: monthName(Date.parse(`${month}-15T00:00:00Z`)), value: u.cost }))} formatY={(y) => `${f.number(y, 0)} €`} />
        </Card>
      </Grid>

      <Card title={w.consumption}>
        <Table
          columns={[
            { key: "season", label: w.season },
            { key: "measured", label: w.measured, align: "right" },
            { key: "used", label: w.used, align: "right" },
            { key: "days", label: w.days, align: "right", optional: true },
          ]}
          rows={(["off", "on", "peak"] as const).map((s) => ({
            id: s,
            season: seasonName(s),
            measured: m.measured[s].days ? `${f.number(m.measured[s].liters / m.measured[s].days, 1)} L/d` : "–",
            used: `${f.number({ off: p.rate_off, on: p.rate_on, peak: p.rate_peak }[s], 1)} L/d`,
            days: f.number(m.measured[s].days, 0),
          }))}
        />
      </Card>

      {flagged.length > 0 && (
        <Card title={w.check}>
          <p className="pg-stat-hint">{w.checkHint}</p>
          <Table columns={[{ key: "date", label: w.date }, { key: "stick", label: w.stickCol, align: "right" }, { key: "liters", label: w.litersCol, align: "right" }]} rows={flagged.map((r) => ({ id: r.id, date: f.date(r.at), stick: `${f.number(r.stick, 1)} cm`, liters: f.number(r.liters, 1) }))} onRowClick={(row) => ctx.open({ itemId: String(row.id) })} />
        </Card>
      )}

      <Grid min={320}>
        <Card title={w.readings} actions={<Button onClick={() => ctx.open({ listId: p.list })}>{w.history}</Button>}>
          <Table columns={[{ key: "date", label: w.date }, { key: "stick", label: w.stickCol, align: "right" }, { key: "liters", label: w.litersCol, align: "right" }, { key: "fill", label: w.fill, align: "right" }]} rows={[...m.readings].reverse().slice(0, 5).map((r) => ({ id: r.id, date: f.date(r.at), stick: `${f.number(r.stick, 1)} cm`, liters: f.number(r.liters, 1), fill: r.percent === undefined ? "…" : `${f.number(r.percent, 1)} %` }))} onRowClick={(row) => ctx.open({ itemId: String(row.id) })} />
        </Card>
        <Card title={w.purchases} actions={<Button onClick={() => ctx.open({ listId: p.list })}>{w.history}</Button>}>
          <Table columns={[{ key: "date", label: w.date }, { key: "liters", label: w.litersCol, align: "right" }, { key: "total", label: w.total, align: "right" }, { key: "price", label: w.perLiter, align: "right" }, { key: "supplier", label: w.supplierCol, optional: true }]} rows={[...m.purchases].reverse().slice(0, 5).map((b) => ({ id: b.id, date: f.date(b.at), liters: f.number(b.liters, 0), total: euro(b.total), price: f.decimal(b.price, 3), supplier: b.supplier || "–" }))} onRowClick={(row) => ctx.open({ itemId: String(row.id) })} />
        </Card>
      </Grid>

      <ReadingDialog w={w} open={dialog === "reading"} onClose={() => setDialog(undefined)} geometry={geometry} />
      <PurchaseDialog w={w} open={dialog === "purchase"} onClose={() => setDialog(undefined)} />
      <HeatingDialog w={w} open={dialog === "heating"} onClose={() => setDialog(undefined)} on={heatingNow.on} assumed={heatingNow.assumed} since={heatingNow.since} />
    </>
  );
}

function ReadingDialog({ w, open, onClose, geometry }: { w: Words; open: boolean; onClose: () => void; geometry?: Geometry }) {
  const ctx = useApp<Params>();
  const f = ctx.format;
  const [stick, setStick] = useState("");
  const { when, setWhen, saving, failed, save: run } = useEntry(open, onClose);
  const value = f.parseDecimal(stick);
  const tooLong = value !== undefined && geometry !== undefined && Number(value) > geometry.stick_length_cm;
  const liters = value !== undefined && geometry && !tooLong ? litersOf(geometry, Number(value)) : undefined;
  const save = () =>
    value !== undefined &&
    !tooLong &&
    run(async () => {
      await ctx.api.v1.resources.byId(ctx.params.list).children.post({ kind: "item", name: `Reading ${when.slice(0, 10)} · ${value} cm`, contentTypeId: ctx.params.reading as never, values: json({ diesel_measured_at: when, diesel_stick_cm: value }) });
      setStick("");
    });
  return (
    <Modal open={open} title={w.readingTitle} onClose={onClose} actions={<><Button onClick={onClose}>{w.cancel}</Button><Button variant="primary" disabled={value === undefined || tooLong || saving} onClick={() => void save()}>{w.save}</Button></>}>
      <NumberField
        autoFocus
        label={w.stick}
        unit="cm"
        value={stick}
        onChange={setStick}
        error={stick && value === undefined ? w.invalid : tooLong ? say(w.outOfRange, { max: geometry!.stick_length_cm }) : undefined}
        hint={liters !== undefined && geometry ? say(w.preview, { p: f.number((liters / geometry.capacity_l) * 100, 0), l: f.number(liters, 0) }) : undefined}
      />
      <DateField label={w.measuredAt} value={when} onChange={setWhen} />
      {failed && <p className="pg-error">{failed}</p>}
    </Modal>
  );
}

function PurchaseDialog({ w, open, onClose }: { w: Words; open: boolean; onClose: () => void }) {
  const ctx = useApp<Params>();
  const f = ctx.format;
  const [liters, setLiters] = useState("");
  const [total, setTotal] = useState("");
  const [supplier, setSupplier] = useState("");
  const { when, setWhen, saving, failed, save: run } = useEntry(open, onClose);
  const l = f.parseDecimal(liters);
  const t = f.parseDecimal(total);
  const save = () =>
    l !== undefined &&
    t !== undefined &&
    run(async () => {
      await ctx.api.v1.resources.byId(ctx.params.list).children.post({
        kind: "item",
        name: `Purchase ${when.slice(0, 10)} · ${l} L`,
        contentTypeId: ctx.params.purchase as never,
        values: json({ diesel_purchased_at: when, diesel_bought_liters: l, diesel_total_price: Number(t), ...(supplier ? { diesel_supplier: supplier } : {}) }),
      });
      setLiters("");
      setTotal("");
      setSupplier("");
    });
  return (
    <Modal open={open} title={w.purchaseTitle} onClose={onClose} actions={<><Button onClick={onClose}>{w.cancel}</Button><Button variant="primary" disabled={l === undefined || t === undefined || saving} onClick={() => void save()}>{w.save}</Button></>}>
      <NumberField autoFocus label={w.bought} unit="L" value={liters} onChange={setLiters} error={liters && l === undefined ? w.invalid : undefined} />
      <NumberField label={w.total} unit="€" value={total} onChange={setTotal} error={total && t === undefined ? w.invalid : undefined} hint={l && t && Number(l) > 0 ? say(w.pricePreview, { price: f.currency(Number(t) / Number(l), "EUR") }) : undefined} />
      <DateField label={w.boughtAt} value={when} onChange={setWhen} />
      <TextField label={w.supplier} value={supplier} onChange={setSupplier} maxLength={200} />
      {failed && <p className="pg-error">{failed}</p>}
    </Modal>
  );
}

function HeatingDialog({ w, open, onClose, on, assumed, since }: { w: Words; open: boolean; onClose: () => void; on: boolean; assumed: boolean; since?: number }) {
  const ctx = useApp<Params>();
  const f = ctx.format;
  const { when, setWhen, saving, failed, save: run } = useEntry(open, onClose);
  const state = on ? w.stateOn : w.stateOff;
  const save = () =>
    run(async () => {
      const next = !on;
      await ctx.api.v1.resources.byId(ctx.params.list).children.post({ kind: "item", name: `Heating ${next ? "on" : "off"} ${when.slice(0, 10)}`, contentTypeId: ctx.params.heating as never, values: json({ diesel_heating_at: when, diesel_heating: next ? "on" : "off" }) });
    });
  return (
    <Modal open={open} title={w.heatingTitle} onClose={onClose} actions={<><Button onClick={onClose}>{w.cancel}</Button><Button variant="primary" disabled={saving} onClick={() => void save()}>{on ? w.turnOff : w.turnOn}</Button></>}>
      <p>{since && !assumed ? say(w.heatingIs, { state, date: f.date(since) }) : say(w.heatingNow, { state, assumed: w.assumed })}</p>
      <DateField label={w.changedAt} value={when} onChange={setWhen} />
      {failed && <p className="pg-error">{failed}</p>}
    </Modal>
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
