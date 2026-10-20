// Indexes each receipt as one chunk of its text plus a model-written
// summary chunk, so a search for the vendor, the items or the total finds
// it. It fills the File content in search role for its library.
import { OperationError, plain, workflow } from "@papergo/workflow";

interface Params {
  library: string;
}

const files = ["application/pdf", "image/*"];
const sources = ["upload", "webdav", "restore", "workflow", "reindex"];

export default workflow<unknown, unknown, Params>({
  triggers: (p) => [
    { type: "file.added", collection_id: p.library, media_types: files, sources },
    { type: "file.updated", collection_id: p.library, media_types: files, sources },
    { type: "manual", collection_id: p.library },
  ],
  concurrency: "coalesce",
  output_schema: {
    type: "object",
    required: ["blob_id"],
    properties: {
      blob_id: { type: "string" }, text_id: { type: "string" }, pages: { type: "integer" },
      language: { type: "string" }, generation: { type: "string" }, chunks: { type: "integer" },
    },
  },
  async run(ctx) {
    const item = ctx.api.v1.items.byId(ctx.itemId!);
    let read;
    try {
      read = await ctx.wait((await item.text.post({ ocr: "auto", maxPages: 10 }))!);
    } catch (e) {
      // A file without readable text has no content to index.
      if (e instanceof OperationError) return { blob_id: (ctx.run.event.data.blob_id as string | undefined) ?? "" };
      throw e;
    }
    const text = plain(read.result) as { text_id: string; blob_id: string; pages: number; chars: number; language?: string };
    const out = { blob_id: text.blob_id, text_id: text.text_id, pages: text.pages, language: text.language };
    if (text.chars === 0) return out;
    const [chunked, summary] = await ctx.wait([
      (await item.search.chunks.post({ textId: text.text_id, blobId: text.blob_id, chunker: "whole" }))!,
      (await ctx.api.v1.ai.completions.post({
        prompt: "Summarize this receipt in two sentences: the vendor, the date, what was bought and the total.",
        itemId: ctx.itemId, textId: text.text_id, blobId: text.blob_id, maxChars: 8000,
      }))!,
    ]);
    const chunks = plain(chunked.result) as { generation: string; chunks: number };
    const answer = plain(summary.result) as { text: string };
    await ctx.wait((await item.search.chunks.post({
      text: answer.text, blobId: text.blob_id, chunker: "whole", kind: "summary", generation: chunks.generation,
    }))!);
    const generation = ctx.api.v1.search.generations.byId(chunks.generation);
    await ctx.wait((await generation.embeddings.post())!);
    await ctx.wait((await generation.publish.post())!);
    return { ...out, generation: chunks.generation, chunks: chunks.chunks + 1 };
  },
});
