// Reads a receipt photo: asks a model in batch mode for the store, date,
// currency, total and product lines, and files the lines in the receipt's own
// folder of its library, each linked to the receipt. It marks the receipt Read
// once the lines are stored, and Needs review when the extraction or the
// filing fails.
import { json, OperationError, plain, workflow } from "@papergo/workflow";
import type { models, PaperGoClient } from "@papergo/workflow";

interface Params {
  library: string;
  receipt_type: string;
  line_type: string;
}

interface Receipt {
  store: string;
  purchase_date: string;
  currency: string;
  total: string;
  lines: { description: string; quantity: string; unit_price: string; amount: string }[];
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
        required: ["description", "quantity", "unit_price", "amount"],
        properties: { description: { type: "string", minLength: 1, maxLength: 200 }, quantity: amount, unit_price: amount, amount },
      },
    },
  },
};

const prompt = "Read this receipt photo. Return only the object described by the schema. store is the seller. purchase_date is YYYY-MM-DD. " +
  "currency is a 3-letter code. total, quantity, unit_price and amount are exact decimal strings, never numbers. One lines entry per product.";

// The relationship that links a receipt to each of its lines.
const contains = "contains receipt line";

const photos = ["image/*"];
const sources = ["upload", "restore", "webdav"];

// Lines are filed in Receipt lines/<receipt ID>/ of the library: a folder per
// receipt, whatever the receipt is named or wherever it is moved, so photos
// and lines never share a folder and line names cannot clash with other
// receipts' lines.
const linesFolder = "Receipt lines";

// update sets values of the item at version, the receipt as it was before
// its image was chosen, and returns its new version. A newer file or edit
// fails it.
async function update(api: PaperGoClient, id: string, version: number, values: Record<string, unknown>): Promise<number> {
  const item = await api.v1.resources.byId(id).patch({ values: json(values) }, { headers: { "If-Match": `"${version}"` } });
  return item!.version!;
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

// lineName numbers a line, so lines with the same description differ, and
// keeps only characters a library name may hold.
function lineName(n: number, description: string): string {
  const text = description.replace(/[\/\\\u0000-\u001f\u007f-\u009f]+/g, " ").trim();
  return text ? `${n}. ${text}` : `${n}`;
}

export default workflow<unknown, unknown, Params>({
  triggers: (p) => [
    // Only receipts: a file saved over a line starts no run.
    { type: "file.added", collection_id: p.library, content_type_id: p.receipt_type, media_types: photos, sources },
    { type: "file.updated", collection_id: p.library, content_type_id: p.receipt_type, media_types: photos, sources },
    { type: "manual", collection_id: p.library, selection: "per_item" },
  ],
  // A newer file for the same item replaces the run that is still going.
  concurrency: "replace",
  async run(ctx) {
    const id = ctx.itemId!;
    const api = ctx.api;
    // A manual start can name any item of the library. Only a receipt with
    // a photo has something to read.
    const item = (await api.v1.resources.byId(id).get())!;
    const types = (await api.v1.resources.byId(ctx.params.library).contentTypes.get())!;
    const listType = (catalogType: string) => types.find((t) => t.parentId === catalogType)?.id;
    if (item.contentTypeId !== listType(ctx.params.receipt_type) || !item.file?.contentType?.startsWith("image/")) {
      return { status: "Not a receipt photo" };
    }
    let version = item.version!;
    try {
      // The model reads the receipt's current photo.
      const image = item.file.blobId!;
      const answer = await ctx.wait((await api.v1.ai.completions.post({
        prompt, itemId: id, imageBlobId: image, execution: "batch", queue: "shared", outputSchema: json(schema),
      }))!);
      const receipt = (plain(answer.result) as { json: Receipt }).json;
      version = await update(api, id, version, { store: receipt.store, purchase_date: receipt.purchase_date, currency: receipt.currency, total: receipt.total });

      // Lines filed by an earlier run go first. Deleting a line hides its
      // link, so the links run out once every linked line is gone, wherever
      // a person moved it.
      for (;;) {
        const page = await api.v1.items.byId(id).relationships.get({ queryParameters: { direction: "outgoing", type: contains } });
        if (!page?.value?.length) break;
        for (const link of page.value) {
          const line = await api.v1.resources.byId(link.targetId!).get();
          await api.v1.resources.byId(link.targetId!).delete({ headers: { "If-Match": `"${line!.version}"` } });
        }
      }
      // A run replaced before it linked a line leaves that line unlinked in
      // the receipt's folder; it goes too.
      const lines = await folder(api, await folder(api, ctx.params.library, linesFolder), id);
      const lineType = listType(ctx.params.line_type);
      for (const old of await children(api, lines, "item")) {
        if (old.contentTypeId === lineType) await api.v1.resources.byId(old.id!).delete({ headers: { "If-Match": `"${old.version}"` } });
      }
      for (const [i, line] of receipt.lines.entries()) {
        const created = await api.v1.resources.byId(lines).children.post({
          kind: "item", name: lineName(i + 1, line.description), contentTypeId: ctx.params.line_type, values: json({ ...line }),
        });
        await api.v1.items.byId(id).relationships.post({ type: contains, directed: true, targetId: created!.id });
      }
      version = (await api.v1.resources.byId(id).get())!.version!;
      await update(api, id, version, { status: "Read" });
      return { status: "Read", lines: receipt.lines.length };
    } catch (e) {
      // A refused answer, a line that cannot be stored or a changed
      // receipt leaves it for a person.
      ctx.warn("needs review:", e instanceof OperationError ? e.message : String(e));
      const current = (await api.v1.resources.byId(id).get())!.version!;
      await update(api, id, current, { status: "Needs review" });
      return { status: "Needs review" };
    }
  },
});
