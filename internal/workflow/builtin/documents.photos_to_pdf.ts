// Combines selected photos, in the chosen order, into one searchable PDF.
// After the owners (or the approvers) review it, the primary item gets the
// PDF as its new file, keeping its metadata and file history, and the other
// photos go to the recycle bin.
import { plain, workflow } from "@papergo/workflow";

interface Params {
  collection: string;
  target_height: number;
  quality: number;
  approvers?: string[];
}

export default workflow<unknown, unknown, Params>({
  input_schema: {
    type: "object",
    properties: {},
    "x-papergo-selection": {
      preview: "image",
      item_label: "page",
      order_label: "Page order",
      primary_description: "This item gets the approved PDF as its new file, keeping its metadata and file history. The other photos go to the recycle bin.",
    },
  },
  triggers: (p) => [{ type: "manual", collection_id: p.collection, selection: "selection" }],
  concurrency: "skip",
  async run(ctx) {
    const p = ctx.params;
    const id = ctx.itemId!;
    const composed = await ctx.wait((await ctx.api.v1.items.byId(id).composePdf.post({
      itemIds: ctx.run.items, targetHeight: p.target_height, quality: p.quality,
    }))!);
    const result = plain(composed.result) as { artifact_id: string };
    const staged = (await ctx.api.v1.artifacts.byId(result.artifact_id).stage.post())!;
    const item = await ctx.api.v1.resources.byId(id).get();
    const review = await ctx.api.v1.workspaces.byId(ctx.run.workspace_id).tasks.post({
      kind: "approval", itemId: id, assignees: p.approvers,
      title: `Review the PDF made from photos for ${item?.name}`,
      description: "The pages are the selected photos in order. Approve to replace the primary item's file with this PDF and move the other photos to the recycle bin.",
      review: { type: "file.preview", stagedFileId: staged.id },
    });
    const answer = plain((await ctx.wait(review!.operationId!)).result) as { outcome: string };
    const file = ctx.api.v1.stagedFiles.byId(staged.id!);
    if (answer.outcome !== "approved") {
      await file.discard.post();
      return { approved: false };
    }
    const promoted = await file.promote.post({ recycleSources: true });
    return { approved: true, blob_id: promoted?.blobId };
  },
});
