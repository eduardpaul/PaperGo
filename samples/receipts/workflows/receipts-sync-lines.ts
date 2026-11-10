// Keeps a receipt's lines in step with the receipt, so spending by category
// counts the same receipts as what was spent: each line holds a copy of its
// receipt's status, currency and date, which it follows when they change.
// Deleting a receipt deletes the folder of its lines, and restoring it brings
// them back. Read receipts files the lines; this workflow changes nothing else.
import { json, workflow } from "@papergo/workflow";
import type { models, PaperGoClient } from "@papergo/workflow";

interface Params {
  library: string;
  receipt_type: string;
  line_type: string;
}

// Where Read receipts files a receipt's lines: Receipt lines/<receipt ID>/.
const linesFolder = "Receipt lines";

// The fields a line copies from its receipt.
const copied = ["receipts_status", "receipts_currency", "receipts_purchase_date"];

// subfolder finds the live folder named name in parent.
async function subfolder(api: PaperGoClient, parent: string, name: string): Promise<models.Resource | undefined> {
  const folders = await api.v1.resources.byId(parent).children.all({ queryParameters: { kind: "folder", top: 500 } });
  return folders.find((f) => f.name?.toLowerCase() === name.toLowerCase());
}

// receiptFolder finds the folder of a receipt's lines, if it has one.
async function receiptFolder(api: PaperGoClient, library: string, id: string): Promise<models.Resource | undefined> {
  const root = await subfolder(api, library, linesFolder);
  return root && subfolder(api, root.id!, id);
}

export default workflow<unknown, unknown, Params>({
  triggers: (p) => [
    { type: "item.updated", collection_id: p.library, content_type_id: p.receipt_type },
    { type: "item.deleted", collection_id: p.library, content_type_id: p.receipt_type },
    { type: "item.restored", collection_id: p.library, content_type_id: p.receipt_type },
  ],
  // A burst of changes reads the receipt as it ends up. A deleted receipt's
  // run has no item, so every deletion gets its own.
  concurrency: "coalesce",
  async run(ctx) {
    const api = ctx.api;
    const { library, line_type: lineType } = ctx.params;
    const id = ctx.itemId ?? ctx.run.event.resource_id!;

    if (ctx.run.event.type === "item.deleted") {
      const folder = await receiptFolder(api, library, id);
      if (!folder) return { status: "No lines" };
      await api.v1.resources.byId(folder.id!).delete({ queryParameters: { recursive: true }, headers: { "If-Match": `"${folder.version}"` } });
      return { status: "Lines deleted" };
    }
    if (ctx.run.event.type === "item.restored") {
      const bin = api.v1.resources.byId(library).recycleBin;
      const deleted = (await bin.all({ queryParameters: { top: 500 } })).find((r) => r.kind === "folder" && r.name === id);
      if (deleted) await bin.byItemID(deleted.id!).restore.post({ queryParameters: { recursive: true } });
    }

    const folder = await receiptFolder(api, library, id);
    if (!folder) return { status: "No lines" };
    const receipt = (await api.v1.resources.byId(id).get())!.values?.additionalData ?? {};
    const types = (await api.v1.resources.byId(library).contentTypes.get())!;
    const listLine = types.find((t) => t.parentId === lineType)?.id;
    let changed = 0;
    for (const line of await api.v1.resources.byId(folder.id!).children.all({ queryParameters: { kind: "item", top: 500 } })) {
      if (line.contentTypeId !== listLine) continue;
      const values = line.values?.additionalData ?? {};
      const differ = copied.filter((key) => (values[key] ?? null) !== (receipt[key] ?? null));
      if (differ.length === 0) continue;
      try {
        await api.v1.resources.byId(line.id!).patch({ values: json(Object.fromEntries(differ.map((key) => [key, receipt[key] ?? null]))) }, { headers: { "If-Match": `"${line.version}"` } });
        changed++;
      } catch (e) {
        // A new reading replaced this line meanwhile; its change starts another run.
        const status = (e as { responseStatusCode?: number }).responseStatusCode;
        if (status !== 404 && status !== 412) throw e;
      }
    }
    return { status: "Lines synced", changed };
  },
});
