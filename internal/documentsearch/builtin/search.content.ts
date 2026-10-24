// Indexes file content: reads the text of a PDF, an image (OCR for scans
// and photos when configured), a text or CSV file, or a Word, Excel or
// PowerPoint file, splits it into chunks, embeds them for
// semantic search when an embedding model is configured, and makes them the
// file's searchable content.
import { OperationError, plain, workflow } from "@papergo/workflow";

interface Params {
  collection: string;
  ocr: "auto" | "always" | "never";
  max_pages: number;
  chunker: "window" | "pages" | "paragraphs" | "headings" | "whole";
  max_chars: number;
  overlap: number;
  max_chunks: number;
  embed: "immediate" | "later";
}

// The kinds of file whose text PaperGo reads; other files start no run.
const files = [
  "application/pdf",
  "image/jpeg",
  "image/png",
  "image/webp",
  "image/tiff",
  "text/markdown",
  "text/plain",
  "text/csv",
  "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
  "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
  "application/vnd.openxmlformats-officedocument.presentationml.presentation",
];
const sources = ["upload", "webdav", "restore", "workflow", "reindex"];

export default workflow<unknown, unknown, Params>({
  triggers: (p) => [
    { type: "file.added", collection_id: p.collection, media_types: files, sources },
    { type: "file.updated", collection_id: p.collection, media_types: files, sources },
    { type: "manual", collection_id: p.collection, selection: "per_item" },
  ],
  concurrency: "coalesce",
  output_schema: {
    type: "object",
    required: ["blob_id"],
    properties: {
      blob_id: { type: "string" },
      text_id: { type: "string" },
      pages: { type: "integer" },
      language: { type: "string" },
      generation: { type: "string" },
      chunks: { type: "integer" },
    },
  },
  async run(ctx) {
    const p = ctx.params;
    const item = ctx.api.v1.items.byId(ctx.itemId!);
    let read;
    try {
      read = await ctx.wait((await item.text.post({ ocr: p.ocr, maxPages: p.max_pages }))!);
    } catch (e) {
      // A file whose text cannot be read has no content to index.
      if (e instanceof OperationError) return { blob_id: (ctx.run.event.data.blob_id as string | undefined) ?? "" };
      throw e;
    }
    const text = plain(read.result) as { text_id: string; blob_id: string; pages: number; chars: number; language?: string };
    const out = { blob_id: text.blob_id, text_id: text.text_id, pages: text.pages, language: text.language };
    if (text.chars === 0) return out;
    const chunked = await ctx.wait((await item.search.chunks.post({
      textId: text.text_id, blobId: text.blob_id, chunker: p.chunker, maxChars: p.max_chars, overlap: p.overlap, maxChunks: p.max_chunks,
    }))!);
    const chunks = plain(chunked.result) as { generation: string; chunks: number };
    const generation = ctx.api.v1.search.generations.byId(chunks.generation);
    if (p.embed === "immediate") await ctx.wait((await generation.embeddings.post())!);
    await ctx.wait((await generation.publish.post())!);
    return { ...out, generation: chunks.generation, chunks: chunks.chunks };
  },
});
