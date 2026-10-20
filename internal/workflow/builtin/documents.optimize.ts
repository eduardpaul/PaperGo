// When an image is added, proposes a smaller WebP copy whose smallest text
// keeps a readable height, and asks the owners (or the approvers) to
// compare both before it replaces the file. A newer file cancels the
// waiting review, because its run replaces this one.
import { plain, workflow } from "@papergo/workflow";

interface Params {
  collection: string;
  target_height: number;
  min_confidence: number;
  percentile: number;
  max_analysis_dimension: number;
  minimum_scale: number;
  quality: number;
  approvers?: string[];
}

export default workflow<unknown, unknown, Params>({
  triggers: (p) => [
    { type: "file.added", collection_id: p.collection },
    { type: "file.updated", collection_id: p.collection },
    { type: "manual", collection_id: p.collection, selection: "per_item" },
  ],
  concurrency: "replace",
  async run(ctx) {
    const p = ctx.params;
    const id = ctx.itemId!;
    const optimized = await ctx.wait((await ctx.api.v1.items.byId(id).optimizeImage.post({
      targetHeight: p.target_height, minConfidence: p.min_confidence, percentile: p.percentile,
      maxAnalysisDimension: p.max_analysis_dimension, minimumScale: p.minimum_scale, quality: p.quality,
      fileVersion: ctx.run.event.data.file_version as number | undefined,
    }))!);
    const result = plain(optimized.result) as { staged_file_id?: string; reason?: string };
    if (!result.staged_file_id) return { kept: result.reason };
    const item = await ctx.api.v1.resources.byId(id).get();
    const review = await ctx.api.v1.workspaces.byId(ctx.run.workspace_id).tasks.post({
      kind: "approval", itemId: id, assignees: p.approvers,
      title: `Review a smaller file for ${item?.name}`,
      description: "The proposed WebP file is shown beside the current file at full size. Approve if its smallest text is still readable.",
      review: { type: "file.compare", stagedFileId: result.staged_file_id },
    });
    const answer = plain((await ctx.wait(review!.operationId!)).result) as { outcome: string };
    const staged = ctx.api.v1.stagedFiles.byId(result.staged_file_id);
    if (answer.outcome !== "approved") {
      await staged.discard.post();
      return { approved: false };
    }
    const promoted = await staged.promote.post({});
    return { approved: true, blob_id: promoted?.blobId };
  },
});
