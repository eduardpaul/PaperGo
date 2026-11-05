// The receipts desk: take a photo of a receipt with the phone, or choose
// some, and the app shrinks each one on the phone before it uploads it to
// the Receipts library. The photo is what the model reads, so it is sent the
// size the model needs, not the camera's: at most MAX_EDGE pixels long, as a
// JPEG, turned upright. The Read receipts workflow then reads it in batch
// mode, files its lines, each with the category of its product, and keeps
// the receipt as a searchable PDF made from the photo. Below, for this
// month, this year or all time: what was spent and on which categories, how
// many receipts wait to be read and need a look, the latest receipts and
// each receipt's lines. The figures are worked out by the server and follow
// new receipts live.
import { useEffect, useMemo, useRef, useState } from "react";
import {
  app,
  Button,
  Card,
  Grid,
  Modal,
  Notice,
  RequestError,
  Stat,
  Table,
  Toolbar,
  useAggregates,
  useApp,
  useNarrow,
  useQuery,
  type Item,
} from "@papergo/app";

interface Params {
  library: string;
  receipt: string;
  line: string;
}

type Period = "month" | "year" | "all";

// The photo the model reads: the long side at most this many pixels, as a
// JPEG of this quality. Vision models scale larger photos down to about
// this size anyway, so more pixels only make the upload slower.
const MAX_EDGE = 2048;
const QUALITY = 0.85;

// The relationship from a receipt to its lines, as the package names it.
const contains = "contains receipt line";

const words = {
  en: {
    take: "Take a photo", choose: "Choose photos", uploads: "Uploads", latest: "Latest receipts",
    optimizing: "Optimizing…", uploading: "Uploading…", uploaded: "Uploaded, waiting to be read", failed: "Failed: {reason}", unreadable: "This photo cannot be opened here; take it again or choose a JPEG",
    shrunk: "{before} → {after}", waiting: "Waiting to be read", read: "Read", review: "Needs review", reviewHint: "The model could not read these. Open one to correct it, or take a better photo.",
    batchHint: "Receipts are read in batches: their lines can take until the nightly batch.",
    receipt: "Receipt", date: "Date", total: "Total", status: "Status", lines: "Lines", description: "Description", quantity: "Qty", unitPrice: "Price", amount: "Amount",
    open: "Open in PaperGo", close: "Close", noLines: "No lines yet", all: "All receipts", setUp: "Choose the Receipts library and its Receipt and Receipt line types in Workspace settings → Apps.",
    statusNew: "Reading", statusRead: "Read", statusReview: "Needs review",
    month: "This month", year: "This year", ever: "All time", spent: "Spent", receiptsCount: "{n} receipts", oneReceipt: "1 receipt", alsoIn: "Also {amounts}",
    byCategory: "Spending by category", category: "Category", share: "{p} %", nothingSpent: "Nothing read in this period yet",
    categories: {} as Record<string, string>,
  },
  es: {
    take: "Hacer una foto", choose: "Elegir fotos", uploads: "Subidas", latest: "Últimos tiques",
    optimizing: "Optimizando…", uploading: "Subiendo…", uploaded: "Subido, esperando a leerse", failed: "Error: {reason}", unreadable: "Esta foto no se puede abrir aquí; hazla otra vez o elige un JPEG",
    shrunk: "{before} → {after}", waiting: "Por leer", read: "Leídos", review: "Por revisar", reviewHint: "El modelo no pudo leerlos. Abre uno para corregirlo, o haz una foto mejor.",
    batchHint: "Los tiques se leen por lotes: sus líneas pueden tardar hasta el lote de la noche.",
    receipt: "Tique", date: "Fecha", total: "Total", status: "Estado", lines: "Líneas", description: "Descripción", quantity: "Cant.", unitPrice: "Precio", amount: "Importe",
    open: "Abrir en PaperGo", close: "Cerrar", noLines: "Aún sin líneas", all: "Todos los tiques", setUp: "Elige la biblioteca Receipts y sus tipos Receipt y Receipt line en Ajustes del espacio → Apps.",
    statusNew: "Leyendo", statusRead: "Leído", statusReview: "Por revisar",
    month: "Este mes", year: "Este año", ever: "Siempre", spent: "Gastado", receiptsCount: "{n} tiques", oneReceipt: "1 tique", alsoIn: "Además {amounts}",
    byCategory: "Gasto por categoría", category: "Categoría", share: "{p} %", nothingSpent: "Aún no hay nada leído en este periodo",
    categories: {
      "Fruit and vegetables": "Fruta y verdura", "Meat and fish": "Carne y pescado", "Dairy and eggs": "Lácteos y huevos", Bakery: "Panadería",
      Pantry: "Despensa", Frozen: "Congelados", Drinks: "Bebidas", "Snacks and sweets": "Aperitivos y dulces", Household: "Hogar",
      "Personal care": "Cuidado personal", Health: "Salud", Baby: "Bebé", Pets: "Mascotas", Other: "Otros",
    } as Record<string, string>,
  },
};
type Words = typeof words.en;

function say(text: string, values: Record<string, string | number>): string {
  return text.replace(/\{(\w+)\}/g, (_, key: string) => String(values[key] ?? ""));
}

// since is the first day of the period, in the viewer's calendar.
function since(period: Period, now = new Date()): string | undefined {
  const two = (n: number) => String(n).padStart(2, "0");
  if (period === "month") return `${now.getFullYear()}-${two(now.getMonth() + 1)}-01`;
  if (period === "year") return `${now.getFullYear()}-01-01`;
  return undefined;
}

function size(bytes: number): string {
  return bytes < 1 << 20 ? `${Math.max(1, Math.round(bytes / 1024))} KB` : `${(bytes / (1 << 20)).toFixed(1)} MB`;
}

// optimize turns a photo upright and shrinks it to the photo the model
// reads. A small JPEG that would not get smaller is sent as it is.
async function optimize(file: File): Promise<Blob> {
  const bitmap = await createImageBitmap(file, { imageOrientation: "from-image" });
  const scale = Math.min(1, MAX_EDGE / Math.max(bitmap.width, bitmap.height));
  const canvas = document.createElement("canvas");
  canvas.width = Math.round(bitmap.width * scale);
  canvas.height = Math.round(bitmap.height * scale);
  const g = canvas.getContext("2d")!;
  // A photo with transparency gets a white page, as paper.
  g.fillStyle = "#fff";
  g.fillRect(0, 0, canvas.width, canvas.height);
  g.drawImage(bitmap, 0, 0, canvas.width, canvas.height);
  bitmap.close();
  const jpeg = await new Promise<Blob | null>((done) => canvas.toBlob(done, "image/jpeg", QUALITY));
  if (!jpeg) throw new Error("the photo could not be encoded");
  return scale === 1 && file.type === "image/jpeg" && file.size <= jpeg.size ? file : jpeg;
}

// multipart is the body of a document upload: the item's metadata and the
// photo, both under the receipt's name. The app sends bytes, not a form,
// through its request bridge.
async function multipart(name: string, photo: Blob): Promise<{ body: ArrayBuffer; type: string }> {
  const boundary = `papergo-${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`;
  const text = new TextEncoder();
  const head = text.encode(
    `--${boundary}\r\nContent-Disposition: form-data; name="metadata"\r\nContent-Type: application/json\r\n\r\n${JSON.stringify({ name })}\r\n` +
      `--${boundary}\r\nContent-Disposition: form-data; name="file"; filename="${name}"\r\nContent-Type: image/jpeg\r\n\r\n`,
  );
  const tail = text.encode(`\r\n--${boundary}--\r\n`);
  const bytes = new Uint8Array(await photo.arrayBuffer());
  const body = new Uint8Array(head.length + bytes.length + tail.length);
  body.set(head, 0);
  body.set(bytes, head.length);
  body.set(tail, head.length + bytes.length);
  return { body: body.buffer, type: `multipart/form-data; boundary=${boundary}` };
}

// receiptName names a receipt after when it was taken, in the viewer's
// clock, as a file: Receipt 2026-11-05 14.32.10.jpg. The workflow gives it
// the PDF's extension once it keeps it as one.
function receiptName(at: Date, copy: number): string {
  const two = (n: number) => String(n).padStart(2, "0");
  const day = `${at.getFullYear()}-${two(at.getMonth() + 1)}-${two(at.getDate())}`;
  const time = `${two(at.getHours())}.${two(at.getMinutes())}.${two(at.getSeconds())}`;
  return `Receipt ${day} ${time}${copy > 1 ? ` (${copy})` : ""}.jpg`;
}

interface Upload {
  key: number;
  name: string;
  state: "optimizing" | "uploading" | "uploaded" | "failed";
  before: number;
  after?: number;
  error?: string;
  itemId?: string;
}

function Desk({ w }: { w: Words }) {
  const ctx = useApp<Params>();
  const f = ctx.format;
  const p = ctx.params;
  const narrow = useNarrow();
  const camera = useRef<HTMLInputElement>(null);
  const gallery = useRef<HTMLInputElement>(null);
  const [uploads, setUploads] = useState<Upload[]>([]);
  const [opened, setOpened] = useState<Item>();
  const next = useRef(0);
  const queue = useRef(Promise.resolve());

  // Receipts only: the folders of their lines have no status.
  const receipts = { contentTypeId: p.receipt, filter: { field: "status", op: "present" } };
  const latest = useQuery(p.library, { ...receipts, sort: [{ field: "$created_at", direction: "desc" }] }, { live: true, top: 20 });
  const counts = useAggregates(p.library, { query: receipts, groups: [{ field: "status" }], aggregates: [{ op: "count", field: "status" }] }, { live: true });
  const count = (status: string) => counts.data.find((g) => g.keys[0]?.value === status)?.count ?? 0;

  // What read receipts of the period cost, by currency; the categories are
  // those of the currency most receipts were paid in. Lines carry their
  // receipt's currency and date for this.
  const [period, setPeriod] = useState<Period>("month");
  const from = since(period);
  const inPeriod = from ? [{ field: "purchase_date", op: "gte", value: from }] : [];
  const spent = useAggregates(
    p.library,
    { query: { contentTypeId: p.receipt, filter: { and: [{ field: "status", op: "eq", value: "Read" }, ...inPeriod] } }, groups: [{ field: "currency" }], aggregates: [{ op: "sum", field: "total", as: "spent" }], sort: [{ field: "$count", direction: "desc" }] },
    { live: true },
  );
  const main = spent.data[0];
  const currency = main ? String(main.keys[0]?.value ?? "") : "";
  const byCategory = useAggregates(
    currency ? p.library : undefined,
    { query: { contentTypeId: p.line, filter: { and: [{ field: "currency", op: "eq", value: currency }, ...inPeriod] } }, groups: [{ field: "category" }], aggregates: [{ op: "sum", field: "amount", as: "spent" }], sort: [{ field: "spent", direction: "desc" }] },
    { live: true },
  );
  const categoryTotal = byCategory.data.reduce((sum, g) => sum + Number(g.values.spent ?? 0), 0);
  const categoryName = (c: unknown) => (c === null || c === undefined ? w.categories.Other ?? "Other" : w.categories[String(c)] ?? String(c));

  const change = (key: number, patch: Partial<Upload>) => setUploads((all) => all.map((u) => (u.key === key ? { ...u, ...patch } : u)));

  // One photo after another, so a phone's upload is not split between them.
  const add = (files: FileList | null) => {
    for (const file of Array.from(files ?? [])) {
      const key = ++next.current;
      const upload: Upload = { key, name: file.name, state: "optimizing", before: file.size };
      setUploads((all) => [upload, ...all].slice(0, 10));
      queue.current = queue.current.then(() => send(key, file));
    }
  };

  const send = async (key: number, file: File) => {
    let photo: Blob;
    try {
      photo = await optimize(file);
    } catch {
      change(key, { state: "failed", error: w.unreadable });
      return;
    }
    change(key, { state: "uploading", after: photo.size });
    const at = new Date();
    // Two photos taken in the same second get numbered names.
    for (let copy = 1; ; copy++) {
      const name = receiptName(at, copy);
      const { body, type } = await multipart(name, photo);
      try {
        const created = await ctx.request<{ item: { id: string } }>("POST", `/v1/resources/${p.library}/documents`, { body, headers: { "content-type": type } });
        change(key, { state: "uploaded", name, itemId: created.data.item.id });
        return;
      } catch (e) {
        if (e instanceof RequestError && e.code === "name_conflict" && copy < 20) continue;
        change(key, { state: "failed", error: e instanceof RequestError ? e.detail ?? e.message : String(e) });
        return;
      }
    }
  };

  const statusName = (status: unknown) => ({ New: w.statusNew, Read: w.statusRead, "Needs review": w.statusReview })[String(status)] ?? String(status ?? "");
  const rows = useMemo(
    () =>
      latest.data.map((r) => {
        const v = r.values;
        return {
          id: r.id,
          receipt: String(v.store ?? r.name),
          date: v.purchase_date ? f.date(String(v.purchase_date)) : f.date(r.created_at),
          total: v.total !== undefined && v.total !== null ? f.currency(String(v.total), String(v.currency ?? "EUR")) : "–",
          status: statusName(v.status),
        };
      }),
    // statusName follows the words.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [latest.data, f, w],
  );

  if (!p.library || !p.receipt || !p.line) return <Notice title={w.receipt}>{w.setUp}</Notice>;

  return (
    <>
      <input ref={camera} type="file" accept="image/*" capture="environment" hidden onChange={(e) => { add(e.target.files); e.target.value = ""; }} />
      <input ref={gallery} type="file" accept="image/*" multiple hidden onChange={(e) => { add(e.target.files); e.target.value = ""; }} />
      <Toolbar>
        <Button variant="primary" onClick={() => camera.current?.click()}>{w.take}</Button>
        <Button onClick={() => gallery.current?.click()}>{w.choose}</Button>
      </Toolbar>

      {uploads.length > 0 && (
        <Card title={w.uploads}>
          <Table
            // The size it was shrunk to shows below the state, on a phone too.
            columns={[{ key: "name", label: w.receipt }, { key: "state", label: w.status, render: (row) => <>{row.state}<div className="pg-stat-hint">{row.size}</div></> }]}
            rows={uploads.map((u) => ({
              id: u.key,
              itemId: u.itemId,
              name: u.name,
              state: u.state === "failed" ? say(w.failed, { reason: u.error ?? "" }) : { optimizing: w.optimizing, uploading: w.uploading, uploaded: w.uploaded }[u.state],
              size: u.after === undefined ? size(u.before) : say(w.shrunk, { before: size(u.before), after: size(u.after) }),
            }))}
            onRowClick={(row) => row.itemId && ctx.open({ itemId: String(row.itemId) })}
          />
        </Card>
      )}

      <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
        {(["month", "year", "all"] as const).map((key) => (
          <Button key={key} variant={period === key ? "primary" : "default"} onClick={() => setPeriod(key)}>
            {{ month: w.month, year: w.year, all: w.ever }[key]}
          </Button>
        ))}
      </div>

      {/* Two figures a row on a phone. */}
      <Grid min={narrow ? 140 : 160}>
        <Stat
          label={w.spent}
          value={main ? f.currency(String(main.values.spent ?? 0), currency) : "–"}
          hint={main ? [main.count === 1 ? w.oneReceipt : say(w.receiptsCount, { n: main.count }), spent.data.length > 1 ? say(w.alsoIn, { amounts: spent.data.slice(1).map((g) => f.currency(String(g.values.spent ?? 0), String(g.keys[0]?.value ?? ""))).join(", ") }) : ""].filter(Boolean).join(" · ") : undefined}
        />
        <Stat label={w.waiting} value={f.number(count("New"), 0)} />
        <Stat label={w.read} value={f.number(count("Read"), 0)} tone="success" />
        <Stat label={w.review} value={f.number(count("Needs review"), 0)} tone={count("Needs review") ? "warning" : "default"} />
      </Grid>

      {count("New") > 0 && <p className="pg-stat-hint">{w.batchHint}</p>}
      {count("Needs review") > 0 && <Notice tone="warning" title={w.review}>{w.reviewHint}</Notice>}

      <Card title={w.byCategory}>
        <Table
          empty={w.nothingSpent}
          columns={[
            { key: "category", label: w.category },
            {
              key: "spent",
              label: w.spent,
              align: "right",
              // A bar for the category's share of the period, under its amount.
              render: (row) => (
                <>
                  {row.spent}
                  <div style={{ height: 6, marginTop: 4, borderRadius: 3, background: "var(--pg-series-1)", width: `${row.pct}%`, marginLeft: "auto" }} />
                </>
              ),
            },
            { key: "share", label: "%", align: "right", optional: true },
          ]}
          rows={byCategory.data.map((g) => {
            const value = Number(g.values.spent ?? 0);
            const pct = categoryTotal ? Math.round((value / categoryTotal) * 100) : 0;
            return { id: String(g.keys[0]?.value ?? "none"), category: categoryName(g.keys[0]?.value), spent: f.currency(value, currency), pct, share: say(w.share, { p: pct }) };
          })}
        />
      </Card>

      <Card title={w.latest} actions={<Button onClick={() => ctx.open({ listId: p.library })}>{w.all}</Button>}>
        <Table
          columns={[{ key: "receipt", label: w.receipt }, { key: "date", label: w.date, optional: true }, { key: "total", label: w.total, align: "right" }, { key: "status", label: w.status }]}
          rows={rows}
          onRowClick={(row) => setOpened(latest.data.find((r) => r.id === row.id))}
        />
      </Card>

      <LinesDialog w={w} receipt={opened} onClose={() => setOpened(undefined)} />
    </>
  );
}

interface Link {
  id: string;
  related_item?: { name?: string; values?: Record<string, unknown> };
}

interface Line {
  id: string;
  n: number;
  values: Record<string, unknown>;
}

// LinesDialog shows a receipt's lines, through the links the workflow made,
// in the order of the receipt: the workflow numbers them "1. Bread".
function LinesDialog({ w, receipt, onClose }: { w: Words; receipt?: Item; onClose: () => void }) {
  const ctx = useApp<Params>();
  const f = ctx.format;
  const [lines, setLines] = useState<Line[]>();
  const id = receipt?.id;
  useEffect(() => {
    setLines(undefined);
    if (!id) return;
    let stale = false;
    const q = new URLSearchParams({ direction: "outgoing", type: contains, $top: "100" });
    ctx
      .request<{ value: Link[] }>("GET", `/v1/items/${id}/relationships?${q}`)
      .then((r) => !stale && setLines(r.data.value.map((l) => ({ id: l.id, n: parseInt(l.related_item?.name ?? "", 10) || 0, values: l.related_item?.values ?? {} })).sort((a, b) => a.n - b.n)))
      .catch(() => !stale && setLines([]));
    return () => {
      stale = true;
    };
  }, [ctx, id]);
  if (!receipt) return null;
  const currency = String(receipt.values.currency ?? "EUR");
  const money = (v: unknown) => (v === undefined || v === null ? "–" : f.currency(String(v), currency));
  const sorted = lines ?? [];
  return (
    <Modal
      open
      title={String(receipt.values.store ?? receipt.name)}
      onClose={onClose}
      actions={<><Button onClick={onClose}>{w.close}</Button><Button variant="primary" onClick={() => ctx.open({ itemId: receipt.id })}>{w.open}</Button></>}
    >
      <p className="pg-stat-hint">
        {[receipt.values.purchase_date ? f.date(String(receipt.values.purchase_date)) : undefined, receipt.values.total !== undefined ? money(receipt.values.total) : undefined].filter(Boolean).join(" · ")}
      </p>
      <Table
        empty={lines === undefined ? "…" : w.noLines}
        columns={[
          // The category under the description, so a phone has room for both.
          { key: "description", label: w.description, render: (row) => <>{row.description}<div className="pg-stat-hint">{row.category}</div></> },
          { key: "quantity", label: w.quantity, align: "right", optional: true },
          { key: "unit", label: w.unitPrice, align: "right", optional: true },
          { key: "amount", label: w.amount, align: "right" },
        ]}
        rows={sorted.map((l) => ({
          id: l.id,
          description: String(l.values.description ?? ""),
          category: w.categories[String(l.values.category ?? "")] ?? String(l.values.category ?? ""),
          quantity: l.values.quantity === undefined ? "–" : f.number(String(l.values.quantity), 3),
          unit: money(l.values.unit_price),
          amount: money(l.values.amount),
        }))}
      />
    </Modal>
  );
}

export default app<Params>({
  parameters: {
    type: "object",
    required: ["library", "receipt", "line"],
    properties: {
      library: { type: "string", title: "Receipts library", "x-papergo": { kind: "collection" } },
      receipt: { type: "string", title: "Receipt type", "x-papergo": { kind: "content_type" } },
      line: { type: "string", title: "Receipt line type", "x-papergo": { kind: "content_type" } },
    },
  },
  render(ctx) {
    const w = ctx.user.preferences.language.startsWith("es") ? words.es : words.en;
    return <Desk w={w} />;
  },
});
