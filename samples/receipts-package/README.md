# Receipts package

A library of receipts and the products on them, a workflow that reads each receipt photo and keeps the receipt as a PDF, and an [app](../../docs/apps.md), the **Receipts desk**, for taking those photos with a phone. Install the workspace by importing the included package. PaperGo has no receipt routes, tables, or upload-time processing: the workflow is an ordinary TypeScript script, [`read-receipts.ts`](read-receipts.ts), and the app an ordinary React screen, [`receipts-app.tsx`](receipts-app.tsx); you can open and change both in their editors.

## From photo to lines

1. **The photo.** The app takes a photo with the phone's camera, or several from its gallery, and shrinks each one on the phone before uploading it to **Receipts**: turned upright, at most 2048 pixels long, as a JPEG of quality 0.85 (`MAX_EDGE` and `QUALITY` in the app). That is about the size a vision model reads, so a 4 MB camera photo uploads as a few hundred KB. The app names it after when it was taken, in the viewer's language, such as `Receipt 2026-11-05 14.32.10.jpg` (`Tique …` in Spanish).
2. **The lines.** The workflow starts when a JPEG, PNG, or WebP file of a receipt is uploaded, restored, or saved over WebDAV, and when you start it by hand. A newer file for the same item replaces the run that is still going (`concurrency: "replace"`). A [batch completion](../../docs/ai.md#batches) reads the photo, with a schema for the store, purchase date, currency, total, and product lines, and for each line the category of its product, one of a fixed list: Fruit and vegetables, Meat and fish, Dairy and eggs, Bakery, Pantry, Frozen, Drinks, Snacks and sweets, Household, Personal care, Health, Baby, Pets and Other. The list is an `enum` in the script's schema and the choices of the line's **Category** field, in the same order; change both together. Amounts are exact decimal strings. Each line also keeps its receipt's currency and purchase date, so spending can be summed by category and period. The workflow writes the receipt's values at the version it read, so a newer file or edit is not overwritten, replaces any lines it filed earlier, and links each new line with **contains receipt line** (one parent receipt per line).
3. **The PDF.** Once the lines are stored, the photo becomes a searchable PDF for keeping, with [Photos to PDF](../../docs/documents.md#photos-to-pdf): scaled so its smallest text stays legible, compressed, with its OCR text so it can be searched. The PDF replaces the photo as the receipt's file, and the receipt takes its extension (`Receipt 2026-11-05 14.32.10.pdf`) unless another file already has that name. Only then is the receipt marked **Read**.

A refused answer, a line that cannot be stored, a photo that cannot become a PDF or a changed receipt marks the receipt **Needs review** and keeps its photo, to be read again. A run started by hand on a receipt kept as a PDF reads the photo it was made from while its bytes are kept: the library keeps no file history, so they are reclaimed an hour after the PDF replaces them. After that, it returns **No photo**. A run started on a line returns **Not a receipt**. Neither changes anything.

**Lines** live in the same library, in a folder per receipt: `Receipt lines/<receipt ID>/`, numbered with their description (`1. Bread`). The folder follows the receipt, not its name or place, so renaming, moving or deleting a receipt never blocks another. A repeated run deletes the receipt's linked lines and every line left in its folder, including one an interrupted run did not link. A file saved over a line starts no run. Over WebDAV, lines appear as empty files under **Receipt lines**, apart from the receipts.

## Install

1. **The workspace.** As an organization administrator, open **Administration → Export and import**, select [`receipts.zip`](receipts.zip), choose **Try it**, then **Import**. This creates the **Receipts desk** workspace with the empty **Receipts** library, which accepts both content types (**Receipt** for uploads by default, and **Receipt line**), fields, saved views (Receipts, Needs review and Lines), relationship policy and the **Read receipts** workflow, its parameters pointing at the imported library and its two content types. No script, token, ID editing or pre-existing workspace is needed. To install another independent copy, enter a different workspace name when importing. Import does not call the model.
2. **The app.** In the workspace, open **Workspace settings → Apps → New app** and paste [`receipts-app.tsx`](receipts-app.tsx).
   - Save it, then choose its parameters with the pickers: the **Receipts** library and its **Receipt** and **Receipt line** types.
   - Set the state to **Published** and save again.
   - The app appears in the sidebar under the library, and in **Apps**. On a phone, its **Take a photo** and **Choose photos** buttons sit at the bottom of the screen.
3. **OCR and the batch model.** Keeping receipts as PDFs needs [OCR](../../docs/ocr.md) (`ONNXRUNTIME_LIB` and `OCR_MODELS`); without it, every receipt ends **Needs review** with its photo. Reading them needs the batch model below.

The importer becomes the workflow's author and the workspace's owner. Runs use that author's permissions. The app acts as whoever opens it, who needs write access to the library to upload. `resources.json`, `read-receipts.ts` and `read-receipts.json` are the editable sources for the package; the [sample package test](../../internal/httpapi/samplepackages_test.go) checks the ZIP through the import API and can rebuild it during development, the [receipts test](../../internal/scriptrun/receipts_test.go) runs the workflow end to end, and the [app compile test](../../internal/apps/compile_test.go) compiles the app.

## The desk

The desk speaks English and Spanish: it follows the viewer's language preference, Spanish for any `es` language and English otherwise, including the product categories. Field names, choices and the parameters form stay as the package names them.

- **Uploads:** each photo in turn, with its size before and after it was shrunk, until it is uploaded and waits to be read. Tapping one opens it in PaperGo.
- **Period:** this month, this year or all time, by purchase date.
- **Figures:** what the period's read receipts cost, in the currency most of them were paid in, with how many there were and the totals in other currencies; and how many receipts wait to be read, are read and need review.
- **Spending by category:** what the period's lines in that currency cost per category, largest first, with a bar for each category's share.
- **Latest receipts:** the 20 most recent, with their store, date, total and status. Tapping one shows its lines in the order of the receipt, each with its category, quantity, unit price and amount, and opens it in PaperGo.

The figures are worked out by the server ([aggregates](../../docs/application-foundation.md)) and follow new and read receipts live. Lines of a receipt marked **Needs review** after some of its lines were filed count until it is read again.

## Batch execution

The script asks for `execution: "batch"` and queue `shared`. `AI_EXECUTION` can stay `immediate`; that deployment default applies only when a request leaves execution empty. Batch mode does not fall back to the chat provider. Configure the [batch provider](../../docs/ai.md#models) on its own, with a model that reads images: an OpenAI-compatible Batch API (OpenAI, Azure OpenAI, Microsoft Foundry) or Anthropic's Message Batches API.

```bash
# OpenAI, Azure OpenAI or Microsoft Foundry
AI_BATCH_PROVIDER=openai
AI_BATCH_MODEL=...
AI_BATCH_ENDPOINT=...          # optional; defaults to the chat endpoint
AI_BATCH_API_KEY_FILE=...      # when the endpoint requires a key

# or Claude
AI_BATCH_PROVIDER=anthropic
AI_BATCH_MODEL=claude-haiku-5-5
AI_BATCH_API_KEY_FILE=...      # a Claude API key
```

The `ai-batch` worker sends a full group of 20 distinct requests at once, and whatever waits at 01:00 UTC. One receipt waits for that flush unless other requests fill a group of 20. Change the group size, a maximum wait or the flush schedule in the `workers.ai-batch` section of `config.yaml`, or send what waits now with `papergo admin workers flush ai-batch`. To send each photo as soon as it waits, admit a group of one:

```yaml
workers:
  ai-batch:
    admission:
      policy: threshold
      threshold: 1
``` The `shared` queue is installation-wide, so other workflows can batch with these receipts. A run waits for its answer without holding a worker. A request that is still open after `AI_BATCH_DEADLINE` (default 72h) fails, which marks the receipt **Needs review**. Repeating the workflow on a receipt reuses the cached answer at once.
