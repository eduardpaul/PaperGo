// Renders a bounded WebP preview of a file when it is added or replaced. The
// preview is of the item's current file, so a run a newer file overtook
// renders that file, which its own run then finds rendered.
import { plain, workflow } from "@papergo/workflow";

interface Params {
  collection: string;
}

// The kinds of file previews render; other files start no run.
const files = ["application/pdf", "image/tiff", "image/jpeg", "image/png", "image/webp"];

export default workflow<unknown, unknown, Params>({
  triggers: (p) => [
    { type: "file.added", collection_id: p.collection, media_types: files },
    { type: "file.updated", collection_id: p.collection, media_types: files },
    { type: "manual", collection_id: p.collection, selection: "per_item" },
  ],
  concurrency: "coalesce",
  async run(ctx) {
    const rendered = await ctx.wait((await ctx.api.v1.items.byId(ctx.itemId!).preview.post())!);
    return plain(rendered.result);
  },
});
