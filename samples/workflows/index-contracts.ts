// Indexes contracts by section: each chunk starts with its heading and
// carries a model-written sentence on which contract and clause it is, so a
// search finds the clause with its page. It fills the File content in
// search role for its library.
import { OperationError, plain, workflow } from "@papergo/workflow";

interface Params {
  library: string;
}

const files = ["application/pdf", "image/*"];
const sources = ["upload", "webdav", "restore", "workflow", "reindex"];
const instructions = "In one sentence: which contract this is, between whom, and which clause the passage is.";

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
      read = await ctx.wait((await item.text.post({ ocr: "auto" }))!);
    } catch (e) {
      // A file without readable text has no content to index.
      if (e instanceof OperationError) return { blob_id: (ctx.run.event.data.blob_id as string | undefined) ?? "" };
      throw e;
    }
    const text = plain(read.result) as { text_id: string; blob_id: string; pages: number; chars: number; language?: string };
    const out = { blob_id: text.blob_id, text_id: text.text_id, pages: text.pages, language: text.language };
    if (text.chars === 0) return out;
    const chunked = plain((await ctx.wait((await item.search.chunks.post({
      textId: text.text_id, blobId: text.blob_id, chunker: "headings", maxChars: 2000,
    }))!)).result) as { generation: string; chunks: number };
    const generation = ctx.api.v1.search.generations.byId(chunked.generation);

    // Each page of sections is described at once, with the contract as the
    // model's input.
    let offset = 0;
    for (;;) {
      const page = await generation.chunks.get({ queryParameters: { offset, limit: 20 } });
      const sections = page?.chunks ?? [];
      if (sections.length > 0) {
        const answers = await ctx.wait(await Promise.all(sections.map(async (section) => (await ctx.api.v1.ai.completions.post({
          prompt: `${instructions}\n\nPassage:\n${section.text}`, itemId: ctx.itemId, textId: text.text_id, blobId: text.blob_id, maxChars: 20000,
        }))!)));
        await generation.enrichments.put({
          enrichments: sections.map((section, i) => ({ part: section.part, ordinal: section.ordinal, text: (plain(answers[i].result) as { text: string }).text.slice(0, 4000) })),
        });
      }
      if (!page?.more) break;
      offset = page.nextOffset!;
    }
    await ctx.wait((await generation.embeddings.post())!);
    await ctx.wait((await generation.publish.post())!);
    return { ...out, generation: chunked.generation, chunks: chunked.chunks };
  },
});
