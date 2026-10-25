// Suggests managed topics for a file with a local decision, asks the
// workspace's owners to review them, and writes the approved topics to the
// item's topics field. When the item changed during the review, the owners
// get a task to run the review again.
import { json, plain, workflow } from "@papergo/workflow";

interface Params {
  library: string;
  term_set: string;
  field: string;
}

export default workflow<unknown, unknown, Params>({
  triggers: (p) => [{ type: "manual", collection_id: p.library }],
  async run(ctx) {
    const p = ctx.params;
    const id = ctx.itemId!;
    const read = plain((await ctx.wait((await ctx.api.v1.items.byId(id).text.post({}))!)).result) as { text_id: string; blob_id: string; chars: number };
    if (read.chars === 0) return { reviewed: false };
    const decided = plain((await ctx.wait((await ctx.api.v1.ai.taxonomyDecisions.post({
      itemId: id, textId: read.text_id, blobId: read.blob_id, termSetId: p.term_set, selection: "multiple", threshold: 0.8,
    }))!)).result) as { term_ids: string[]; source: { item_version: number } };
    if (decided.term_ids.length === 0) return { reviewed: false };
    const item = await ctx.api.v1.resources.byId(id).get();
    const answer = await ctx.ask<{ terms: string[] }>({
      kind: "approval", itemId: id, assignees: ["role:owners"], title: `Review topics for ${item!.name}`,
      inputSchema: {
        type: "object", required: ["terms"],
        properties: { terms: { type: "array", title: "Approved topics", uniqueItems: true, items: { type: "string" }, "x-papergo": { kind: "terms", term_set_id: p.term_set } } },
      },
      values: { terms: decided.term_ids },
    });
    if (answer.outcome !== "approved") return { reviewed: true, applied: false };
    try {
      // The version the decision read: a newer file or edit refuses it.
      await ctx.api.v1.resources.byId(id).patch({ values: json({ [p.field]: answer.input!.terms }) }, { headers: { "If-Match": `"${decided.source.item_version}"` } });
    } catch {
      await ctx.api.v1.workspaces.byId(ctx.run.workspace_id).tasks.post({
        itemId: id, assignees: ["role:owners"], title: `Classification needs recomputation for ${item!.name}`,
        description: "The file or the item's metadata changed during review. Run Taxonomy review again to get new suggestions.",
      });
      return { reviewed: true, applied: false };
    }
    return { reviewed: true, applied: true };
  },
});
