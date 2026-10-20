// Reads a receipt photo: keeps a smaller copy when one is suitable, asks a
// model in batch mode for the store, date, currency, total and product
// lines, and files the lines in the lines list, each linked to the receipt.
// It marks the receipt Read once the lines are stored, and Needs review
// when the extraction or the filing fails.
import { json, OperationError, plain, workflow } from "@papergo/workflow";
import type { PaperGoClient } from "@papergo/workflow";

interface Params {
  library: string;
  lines: string;
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

// update sets values of the item at version, the receipt as it was when its
// image was chosen, and returns its new version. A newer file or edit
// fails it.
async function update(api: PaperGoClient, id: string, version: number, values: Record<string, unknown>): Promise<number> {
  const item = await api.v1.resources.byId(id).patch({ values: json(values) }, { headers: { "If-Match": `"${version}"` } });
  return item!.version!;
}

export default workflow<unknown, unknown, Params>({
  triggers: (p) => [
    { type: "file.added", collection_id: p.library, media_types: photos, sources },
    { type: "file.updated", collection_id: p.library, media_types: photos, sources },
    { type: "manual", collection_id: p.library, selection: "per_item" },
  ],
  // A newer file for the same item replaces the run that is still going.
  concurrency: "replace",
  async run(ctx) {
    const id = ctx.itemId!;
    const api = ctx.api;
    let version: number;
    try {
      const optimized = plain((await ctx.wait((await api.v1.items.byId(id).optimizeImage.post({}))!)).result) as { staged_file_id?: string; blob_id: string };
      let image = optimized.blob_id;
      if (optimized.staged_file_id) {
        image = (await api.v1.stagedFiles.byId(optimized.staged_file_id).promote.post({}))!.blobId!;
      }
      version = (await api.v1.resources.byId(id).get())!.version!;
      const answer = await ctx.wait((await api.v1.ai.completions.post({
        prompt, itemId: id, imageBlobId: image, execution: "batch", queue: "shared", outputSchema: json(schema),
      }))!);
      const receipt = (plain(answer.result) as { json: Receipt }).json;
      version = await update(api, id, version, { store: receipt.store, purchase_date: receipt.purchase_date, currency: receipt.currency, total: receipt.total });

      // Lines filed by an earlier run go first, a page at a time until none
      // is left.
      for (;;) {
        const page = await api.v1.items.byId(id).relationships.get({ queryParameters: { direction: "outgoing", type: contains } });
        if (!page?.value?.length) break;
        for (const link of page.value) {
          await api.v1.items.byId(id).relationships.byLinkID(link.id!).delete();
          const line = await api.v1.resources.byId(link.targetId!).get();
          await api.v1.resources.byId(link.targetId!).delete({ headers: { "If-Match": `"${line!.version}"` } });
        }
      }
      for (const line of receipt.lines) {
        const created = await api.v1.resources.byId(ctx.params.lines).children.post({
          kind: "item", name: line.description, contentTypeId: ctx.params.line_type, values: json({ ...line }),
        });
        await api.v1.items.byId(id).relationships.post({ type: contains, directed: true, targetId: created!.id });
      }
      if (optimized.staged_file_id) {
        // The replaced photo goes once the grace period ends.
        await api.v1.items.byId(id).content.reclaim.post({ blobId: optimized.blob_id });
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
