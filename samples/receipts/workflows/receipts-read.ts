// Reads a receipt photo: shrinks it first so its smallest text keeps a
// readable height, asks a model in batch mode for the store, date,
// currency, total and product lines it shows, each line with the category of
// its product from a fixed list, files the lines in the
// receipt's own folder of its library, each linked to the receipt, and then
// keeps the receipt as a searchable PDF made from the photo. It marks the
// receipt Read once the lines and the PDF are stored, and Needs review when
// the extraction, the filing or the conversion fails, leaving the photo to
// read again, saying why in the receipt's Problem field.
import { json, OperationError, plain, workflow } from "@papergo/workflow";
import type { models, PaperGoClient } from "@papergo/workflow";

interface Params {
  library: string;
  receipt_type: string;
  line_type: string;
}

// The kinds of product a line is filed under: the choices of the line's
// category field, in the same order. Change both together.
const categories = [
  "Fruit and vegetables", "Meat and fish", "Dairy and eggs", "Bakery", "Pantry", "Frozen", "Drinks",
  "Snacks and sweets", "Household", "Personal care", "Health", "Baby", "Pets", "Other",
];

interface Receipt {
  store: string;
  purchase_date: string;
  currency: string;
  total: string;
  lines: { description: string; category: string; quantity: string; unit_price: string; amount: string }[];
}

const amount = { type: "string", minLength: 1, maxLength: 40 };
const schema = {
  type: "object",
  required: ["store", "purchase_date", "currency", "total", "lines"],
  properties: {
    store: { type: "string", minLength: 1, maxLength: 200 },
    purchase_date: { type: "string", minLength: 10, maxLength: 10 },
    currency: { type: "string", minLength: 3, maxLength: 3 },
    total: amount,
    lines: {
      type: "array", minItems: 1, maxItems: 100,
      items: {
        type: "object",
        required: ["description", "category", "quantity", "unit_price", "amount"],
        properties: {
          description: { type: "string", minLength: 1, maxLength: 200 },
          category: { type: "string", enum: categories },
          quantity: amount, unit_price: amount, amount,
        },
      },
    },
  },
};

const prompt = "Read this receipt photo. Return only the object described by the schema. store is the seller. purchase_date is YYYY-MM-DD. " +
  "currency is a 3-letter code. total, quantity, unit_price and amount are exact decimal strings, never numbers, with a dot as the decimal " +
  "separator and no thousands separator or currency symbol, whatever the receipt shows: 1234.50, not 1.234,50 or 1,234.50 EUR. One lines entry per product. " +
  "category is the kind of product, one of the listed values; Other when none fits.";

// An optimized photo replaces the uploaded one only when it saves at least
// this share of its bytes: a photo shrunk before, by this workflow or by the
// app, is kept rather than encoded again.
const optimizedAtMost = 0.8;

// The relationship that links a receipt to each of its lines.
const contains = "contains receipt line";

// The photos the model reads and the PDF is made from.
const photos = ["image/jpeg", "image/png", "image/webp"];
const sources = ["upload", "restore", "webdav"];

// Lines are filed in Receipt lines/<receipt ID>/ of the library: a folder per
// receipt, whatever the receipt is named or wherever it is moved, so photos
// and lines never share a folder and line names cannot clash with other
// receipts' lines.
const linesFolder = "Receipt lines";

// The solution's fields start with its key: receipts_store, receipts_total…
// The model's answer and this script name them without it; fields() adds it
// where values are written, and leaves a name that already has it.
const prefix = "receipts_";
const fields = (values: Record<string, unknown>): Record<string, unknown> =>
  Object.fromEntries(Object.entries(values).map(([key, value]) => [key.startsWith(prefix) ? key : prefix + key, value]));

// update sets values of the item at version, the receipt as it was when its
// text was read, and returns its new version. A newer file or edit fails it.
async function update(api: PaperGoClient, id: string, version: number, values: Record<string, unknown>): Promise<number> {
  const item = await api.v1.resources.byId(id).patch({ values: json(fields(values)) }, { headers: { "If-Match": `"${version}"` } });
  return item!.version!;
}

// removeLine deletes a line at the version it has now: the receipt's status
// changing makes Sync receipt lines change it meanwhile.
async function removeLine(api: PaperGoClient, id: string): Promise<void> {
  for (let tries = 0; ; tries++) {
    try {
      const line = await api.v1.resources.byId(id).get();
      await api.v1.resources.byId(id).delete({ headers: { "If-Match": `"${line!.version}"` } });
      return;
    } catch (e) {
      const status = (e as { responseStatusCode?: number }).responseStatusCode;
      if (status === 404) return;
      if (status !== 412 || tries >= 5) throw e;
    }
  }
}

// renamePdf gives a receipt named after its photo the PDF's extension, and
// returns its version. A name another file has already keeps the old one.
async function renamePdf(api: PaperGoClient, id: string): Promise<number> {
  const item = (await api.v1.resources.byId(id).get())!;
  const name = item.name!.replace(/\.(jpe?g|png|webp)$/i, ".pdf");
  if (name === item.name) return item.version!;
  try {
    const renamed = await api.v1.resources.byId(id).patch({ name }, { headers: { "If-Match": `"${item.version}"` } });
    return renamed!.version!;
  } catch {
    return (await api.v1.resources.byId(id).get())!.version!;
  }
}

// children lists the direct children of parent of one kind, every page.
async function children(api: PaperGoClient, parent: string, kind: "item" | "folder"): Promise<models.Resource[]> {
  const out: models.Resource[] = [];
  let skiptoken: string | undefined;
  do {
    const page = await api.v1.resources.byId(parent).children.get({ queryParameters: { kind, top: 500, skiptoken } });
    out.push(...(page?.value ?? []));
    const next = page?.odataNextLink?.match(/[?&]\$skiptoken=([^&]+)/)?.[1];
    skiptoken = next ? decodeURIComponent(next) : undefined;
  } while (skiptoken);
  return out;
}

// folder returns the folder named name in parent, and creates it when there
// is none. A run on another receipt may create it first.
async function folder(api: PaperGoClient, parent: string, name: string): Promise<string> {
  const find = async () => (await children(api, parent, "folder")).find((f) => f.name?.toLowerCase() === name.toLowerCase())?.id ?? undefined;
  const found = await find();
  if (found) return found;
  try {
    return (await api.v1.resources.byId(parent).children.post({ kind: "folder", name }))!.id!;
  } catch (e) {
    const raced = await find();
    if (raced) return raced;
    throw e;
  }
}

// A number as a receipt prints it: digits with dots, commas, and spaces
// before a group of three digits (1 234,50), from a separator before the
// first digit (.50) on. Text around it, a currency or a unit, is not part of it.
const numberPattern = /[.,]?\d(?:[\d.,]|[ \u00a0\u202f](?=\d{3}(?!\d)))*/g;

// decimal is an amount the model read, as a field of scale decimals takes it.
// The prompt asks for 1234.50, but a model can copy the receipt's 12,50,
// 1.234,50 EUR, 12,50-, 0,846 kg or 2,25 €/kg: the last separator is the
// decimal one, unless it repeats, as in 1,234,567, and a currency or unit
// around the number is left out. Text holding more than one number, such as
// 2 x 4,50, is not an amount, nor are digits past scale, which are not
// rounded: an amount this cannot read exactly throws, and the receipt goes to
// review instead of storing a wrong amount.
function decimal(text: string, scale: number): string {
  const numbers = text.match(numberPattern) ?? [];
  if (numbers.length !== 1) throw new Error(`"${text}" is not one amount`);
  const digits = numbers[0].replace(/[ \u00a0\u202f]/g, "").replace(/[.,]+$/, "");
  if (!/\d/.test(digits)) throw new Error(`"${text}" is not an amount`);
  const last = Math.max(digits.lastIndexOf("."), digits.lastIndexOf(","));
  const point = last >= 0 && digits.indexOf(digits[last]) === last;
  const whole = (point ? digits.slice(0, last) : digits).replace(/[.,]/g, "").replace(/^0+(?=\d)/, "") || "0";
  const fraction = (point ? digits.slice(last + 1) : "").replace(/0+$/, "");
  if (fraction.length > scale) throw new Error(`"${text}" has more than ${scale} decimals`);
  // A minus next to the number, or with only a currency between, as in -4,50,
  // € -4,50, -€4,50 or 4,50 €-. A hyphen elsewhere, as in Coca-Cola 1,50 or
  // 12,50 EUR - IVA incl., is not a sign; nor is one before zero.
  const at = text.indexOf(numbers[0]);
  const negative =
    (/[-\u2212]\s*(?:[€$£¥]|[A-Z]{3})?\s*$/.test(text.slice(0, at)) ||
      /^\s*(?:[€$£¥]|[A-Z]{3})?\s*[-\u2212]\s*$/.test(text.slice(at + numbers[0].length))) &&
    (whole !== "0" || fraction !== "");
  return `${negative ? "-" : ""}${whole}${fraction && `.${fraction}`}`;
}

// readable checks the answer and puts it as the fields take it, so nothing of
// a receipt the fields would refuse is stored. Change the scales together
// with resources.json.
function readable(r: Receipt): Receipt {
  const day = /^\d{4}-\d{2}-\d{2}$/.test(r.purchase_date) && !Number.isNaN(Date.parse(r.purchase_date));
  if (!day) throw new Error(`"${r.purchase_date}" is not a date`);
  const currency = r.currency.toUpperCase();
  if (!/^[A-Z]{3}$/.test(currency)) throw new Error(`"${r.currency}" is not a currency`);
  return {
    ...r, currency, total: decimal(r.total, 2),
    lines: r.lines.map((l) => ({ ...l, quantity: decimal(l.quantity, 4), unit_price: decimal(l.unit_price, 4), amount: decimal(l.amount, 2) })),
  };
}

// lineName numbers a line, so lines with the same description differ, and
// keeps only characters a library name may hold.
function lineName(n: number, description: string): string {
  const text = description.replace(/[\/\\\u0000-\u001f\u007f-\u009f]+/g, " ").trim();
  return text ? `${n}. ${text}` : `${n}`;
}

export default workflow<unknown, unknown, Params>({
  triggers: (p) => [
    // Only receipts: a file saved over a line starts no run. Every kind of
    // file does, so that one the model cannot read does not wait forever.
    { type: "file.added", collection_id: p.library, content_type_id: p.receipt_type, sources },
    { type: "file.updated", collection_id: p.library, content_type_id: p.receipt_type, sources },
    { type: "manual", collection_id: p.library, selection: "per_item" },
  ],
  // A newer file for the same item replaces the run that is still going.
  concurrency: "replace",
  async run(ctx) {
    const id = ctx.itemId!;
    const api = ctx.api;
    // A manual start can name any item of the library. Only a receipt with
    // a photo has something to read: its current file, or, once it is kept
    // as a PDF, the photo it was made from until its bytes are reclaimed
    // (the optimized one, the newest).
    const item = (await api.v1.resources.byId(id).get())!;
    const types = (await api.v1.resources.byId(ctx.params.library).contentTypes.get())!;
    const listType = (catalogType: string) => types.find((t) => t.parentId === catalogType)?.id;
    if (item.contentTypeId !== listType(ctx.params.receipt_type)) return { status: "Not a receipt" };
    const versions = (await api.v1.items.byId(id).content.versions.get())?.value ?? [];
    // A file just saved that is no photo is not read in place of an older
    // photo: it is what the receipt holds now.
    const saved = ctx.run.event.type.startsWith("file.") ? String(ctx.run.event.data.content_type) : undefined;
    const photo = saved && !photos.includes(saved) ? undefined : versions
      .filter((v) => photos.includes(v.contentType ?? "") && v.availability !== "reclaimed")
      .sort((a, b) => Number(b.isCurrent) - Number(a.isCurrent) || b.version! - a.version!)[0];
    if (!photo) {
      // One that never had a photo is not waiting to be read: it is for a person.
      if (item.values?.additionalData?.receipts_status === "New") await update(api, id, item.version!, { status: "Needs review", problem: "Not a photo" });
      return { status: "No photo" };
    }
    let version = item.version!;
    let problem = "Not readable";
    try {
      // An answer is cached by its request, so reading a receipt in review
      // again says why this run asks: the same request would get the answer
      // that sent it to review.
      const again = item.values?.additionalData?.receipts_status === "Needs review";
      // While it is read it waits to be read, whatever it was before.
      if (item.values?.additionalData?.receipts_status !== "New") version = await update(api, id, version, { status: "New", problem: null });
      // The model reads the photo shrunk so its smallest text keeps 32
      // pixels, as the PDF keeps it: whatever uploaded it, a camera photo
      // of several MB goes to the model as a few hundred KB. It replaces the
      // uploaded photo as the receipt's file; a file a workflow saves starts
      // no run.
      let read = photo.id!;
      if (photo.isCurrent) {
        const optimized = await ctx.wait((await api.v1.items.byId(id).optimizeImage.post({}))!);
        const result = plain(optimized.result) as { artifact_id: string; bytes: number };
        const staged = (await api.v1.artifacts.byId(result.artifact_id).stage.post())!;
        if (result.bytes <= optimizedAtMost * (photo.size ?? 0)) {
          read = (await api.v1.stagedFiles.byId(staged.id!).promote.post({}))!.blobId!;
          version = (await api.v1.resources.byId(id).get())!.version!;
        } else {
          await api.v1.stagedFiles.byId(staged.id!).discard.post();
        }
      }
      const answer = await ctx.wait((await api.v1.ai.completions.post({
        prompt, itemId: id, imageBlobId: read, execution: "batch", queue: "shared", outputSchema: json(schema),
        input: again ? `An earlier answer could not be used; read the receipt again carefully. (${ctx.run.id})` : undefined,
      }))!);
      const receipt = readable((plain(answer.result) as { json: Receipt }).json);
      problem = "Not filed";
      version = await update(api, id, version, { store: receipt.store, purchase_date: receipt.purchase_date, currency: receipt.currency, total: receipt.total });

      // Lines filed by an earlier run go first. Deleting a line hides its
      // link, so the links run out once every linked line is gone, wherever
      // a person moved it.
      for (;;) {
        const page = await api.v1.items.byId(id).relationships.get({ queryParameters: { direction: "outgoing", type: contains } });
        if (!page?.value?.length) break;
        for (const link of page.value) await removeLine(api, link.targetId!);
      }
      // A run replaced before it linked a line leaves that line unlinked in
      // the receipt's folder; it goes too.
      const lines = await folder(api, await folder(api, ctx.params.library, linesFolder), id);
      const lineType = listType(ctx.params.line_type);
      for (const old of await children(api, lines, "item")) {
        if (old.contentTypeId === lineType) await removeLine(api, old.id!);
      }
      for (const [i, line] of receipt.lines.entries()) {
        const created = await api.v1.resources.byId(lines).children.post({
          // Each line also carries its receipt's currency and date, so
          // spending can be summed by category and period.
          kind: "item", name: lineName(i + 1, line.description), contentTypeId: ctx.params.line_type,
          values: json(fields({ ...line, currency: receipt.currency, purchase_date: receipt.purchase_date })),
        });
        await api.v1.items.byId(id).relationships.post({ type: contains, directed: true, targetId: created!.id });
      }
      // The receipt is kept as a searchable PDF of the photo the model read,
      // compressed, with its OCR text. It replaces the photo as the
      // receipt's file.
      problem = "No PDF";
      if (photo.isCurrent) {
        const composed = await ctx.wait((await api.v1.items.byId(id).composePdf.post({}))!);
        const artifact = (plain(composed.result) as { artifact_id: string }).artifact_id;
        const staged = (await api.v1.artifacts.byId(artifact).stage.post())!;
        await api.v1.stagedFiles.byId(staged.id!).promote.post({});
      }
      version = await renamePdf(api, id);
      await update(api, id, version, { status: "Read" });
      return { status: "Read", lines: receipt.lines.length };
    } catch (e) {
      // A refused answer, a line that cannot be stored, a photo that cannot
      // become a PDF or a changed receipt leaves it for a person.
      ctx.warn("needs review:", e instanceof OperationError ? e.message : String(e));
      const current = (await api.v1.resources.byId(id).get())!.version!;
      const changed = (e as { responseStatusCode?: number }).responseStatusCode === 412;
      await update(api, id, current, { status: "Needs review", problem: changed ? "Changed" : problem });
      return { status: "Needs review" };
    }
  },
});
