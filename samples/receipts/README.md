# Receipts

A library of receipts and the products on them, a workflow that reads each receipt photo and keeps the receipt as a PDF, another that keeps the lines in step with their receipt, and an [app](../../docs/apps.md), the **Receipts desk**, for taking those photos with a phone. It is a [solution](../../docs/solutions.md), applied to a workspace that exists. PaperGo has no receipt routes, tables, or upload-time processing: the workflows are ordinary TypeScript scripts, [`read-receipts.ts`](read-receipts.ts) and [`sync-receipt-lines.ts`](sync-receipt-lines.ts), and the app an ordinary React screen, [`receipts-app.tsx`](receipts-app.tsx); you can open and change them in their editors.

## From photo to lines

1. **The photo.** The app takes a photo with the phone's camera, or several from its gallery, and shrinks each one on the phone before uploading it to **Receipts**: turned upright, at most 2048 pixels long, as a JPEG of quality 0.85 (`MAX_EDGE` and `QUALITY` in the app), without the camera's metadata, such as where it was taken. That is about the size a vision model reads, so a 4 MB camera photo uploads as a few hundred KB. A photo uploaded any other way is shrunk by the workflow before the model reads it. The app names it after when it was taken, in the viewer's language, such as `Receipt 2026-11-05 14.32.10.jpg` (`Tique …` in Spanish).
2. **The lines.** The workflow starts when a file of a receipt is uploaded, restored, or saved over WebDAV, and when you start it by hand. It reads JPEG, PNG and WebP photos; a receipt that gets any other file, such as an iPhone's HEIC over WebDAV or a PDF, goes to **Needs review** rather than wait to be read forever. A newer file for the same item replaces the run that is still going (`concurrency: "replace"`). First it [makes the photo a JPEG](../../docs/documents.md#a-photo-as-jpeg) whose smallest text is 32 pixels high, in color, and makes that JPEG the receipt's file when it saves at least a fifth of the photo's bytes; a photo shrunk before, by the app or an earlier run, stays as it is. Whatever uploaded it, a camera photo of several MB goes to the model as a few hundred KB. A [batch completion](../../docs/ai.md#batches) then reads it, with a schema for the store, purchase date, currency, total, and product lines, and for each line the category of its product, one of a fixed list: Fruit and vegetables, Meat and fish, Dairy and eggs, Bakery, Pantry, Frozen, Drinks, Snacks and sweets, Household, Personal care, Health, Baby, Pets and Other. The list is an `enum` in the script's schema and the choices of the line's **Category** field, in the same order; change both together. Amounts are exact decimal strings with a dot. An answer that copies the receipt's `12,50`, `1.234,50 €`, `4,50-` or `0,846 kg` is read as the amount it means, leaving out the currency or unit, and takes a minus only next to the number, so `12,50 € - IVA incl.` stays positive; one that holds two numbers, such as `2 x 4,50`, is not read as one, and the receipt goes to **Needs review**; quantities and unit prices keep four decimals, for fuel's 1.459 a litre, and totals and line amounts two. Each line also keeps its receipt's currency and purchase date, so spending can be summed by category and period. The workflow writes the receipt's values at the version it read, so a newer file or edit is not overwritten, replaces any lines it filed earlier, and links each new line with **contains receipt line** (one parent receipt per line).
3. **The PDF.** Once the lines are stored, the photo the model read becomes a PDF for keeping, with [Photos to PDF](../../docs/documents.md#photos-to-pdf): at the size the model read, in gray, with its OCR text so it can be searched. The PDF replaces the photo as the receipt's file, and the receipt takes its extension (`Receipt 2026-11-05 14.32.10.pdf`) unless another file already has that name. Only then is the receipt marked **Read**.

A refused answer, an amount or date that cannot be read exactly, a photo that cannot be shrunk, a line that cannot be stored, a photo that cannot become a PDF or a changed receipt marks the receipt **Needs review** with the reason in its **Problem** field, and keeps its photo. While a receipt is read it shows as waiting, whatever it was before. Reading it again asks the model again rather than reuse the answer that failed. A run started by hand on a receipt kept as a PDF reads the photo it was made from while its bytes are kept: the library keeps no file history, so they are reclaimed an hour after the PDF replaces them. After that, it returns **No photo**. A run started on a line returns **Not a receipt**. Neither changes anything, unless the receipt was still waiting to be read.

**Lines** live in the same library, in a folder per receipt: `Receipt lines/<receipt ID>/`, numbered with their description (`1. Bread`). The folder follows the receipt, not its name or place, so renaming, moving or deleting a receipt never blocks another. A repeated run deletes the receipt's linked lines and every line left in its folder, including one an interrupted run did not link. A file saved over a line starts no run. Over WebDAV, lines appear as empty files under **Receipt lines**, apart from the receipts.

**Lines follow their receipt.** The **Sync receipt lines** workflow copies a receipt's status, currency and purchase date to its lines whenever they change, so correcting a receipt's date moves its lines to the same period, and a line counts in spending only while its receipt is **Read**. Deleting a receipt deletes the folder of its lines to the recycle bin, and restoring it brings them back.

## Settings

The solution's settings choose how photos are read and kept; the PDF is made from the photo the model read, at its size.

| Setting | Default | Reaches |
|---|---|---|
| `read_text_height` | 32 | the height in pixels the smallest text of the photo the model reads keeps |
| `read_quality` | 80 | that photo's JPEG quality |
| `pdf_color` | `gray` | the PDF the receipt is kept as: `gray`, smaller, or `rgb` |
| `pdf_quality` | 80 | the PDF's JPEG quality |

## Install

1. **Apply the [solution](../../docs/solutions.md)** to a workspace that exists, as someone who manages it and has `solution.manage`:

   ```sh
   papergo solution apply ./samples/receipts --workspace Home
   ```

   This creates, in that workspace:
   - the empty **Receipts** library, which accepts both content types (**Receipt** for uploads by default, and **Receipt line**), with their fields, saved views (Receipts, Needs review and Lines) and relationship policy;
   - the **Read receipts** and **Sync receipt lines** workflows, their parameters pointing at the library and its two content types;
   - the **Receipts desk** app, published, with its parameters set to the same library and types. It appears in the sidebar under the library, and in **Apps**. On a phone, its **Take a photo** and **Choose photos** buttons sit at the bottom of the screen.

   Applying does not call the model. It installs beside other solutions, such as the [diesel tracker](../diesel/README.md), in the same workspace.
2. **The batch model and OCR.** The solution requires a batch provider, which reads the receipts: PaperGo refuses to install it without one. See [Batch execution](#batch-execution) below. It names [OCR](../../docs/ocr.md) (`ONNXRUNTIME_LIB` and `OCR_MODELS`) as what it works in part without, and its status says when OCR is missing: the lines are still read, but no receipt becomes a PDF, so each ends **Needs review** with its photo.

Whoever applies it becomes the author of the workflows and the app. Runs use that author's permissions. The app acts as whoever opens it, who needs write access to the library to upload. [`solution.json`](solution.json), the workflows in [`workflows/`](workflows) and the app in [`apps/`](apps) are the editable sources: the folder is the solution. Its fields start with its key (`receipts_store`, `receipts_total`); the model's answer names them without it, and the scripts add it where values are written. The [solutions test](../../internal/solutions/lifecycle_test.go) installs it, the [receipts test](../../internal/scriptrun/receipts_test.go) runs the workflows end to end, and the [app compile test](../../internal/appcode/compile_test.go) compiles the app.

## The desk

The desk speaks English and Spanish: it follows the viewer's language preference, Spanish for any `es` language and English otherwise. It shows the product categories in that language too, though the line's **Category** field stores them as the package names them, in English, as it does its other choices. PaperGo's own screens show the fields, their choices and the app's parameters form as the package names them.

- **Uploads:** each photo in turn, with its size before and after it was shrunk, until it is uploaded and waits to be read. A photo that cannot reach PaperGo is kept and goes again when the phone is back online, every 30 seconds, or when you tap **Try now**. Tapping an uploaded one opens it in PaperGo. Photos not sent yet live only in this screen: while there are any it says to keep it open, and opening something in PaperGo from the desk asks first. Leaving through PaperGo's own menus does not ask, as the desk cannot see them.
- **Period:** this month, this year or all time, by purchase date.
- **Figures:** what the period's read receipts cost, in the currency most of them were paid in, with how many there were and the totals in other currencies; how many receipts of the period are read; and how many wait to be read or need review, whatever their date, as those that have not been read may have none. A figure not known yet, or that could not be worked out, shows `–`.
- **Spending by category:** what the lines of the period's read receipts in that currency cost per category, largest first, with a bar for each category's share. It sums the lines, so a receipt whose total differs from them, such as by a discount the model did not list as a line, differs from **Spent** by as much.
- **Latest receipts:** the 20 most recent, with their store, date, total and status. Tapping one shows its status and its lines in the order of the receipt, each with its category, quantity, unit price and amount, and opens it in PaperGo.
- **Needs review:** when receipts need review, a notice says so and **Show them** lists them in place of the latest, however old they are. Tapping one says what went wrong and offers:
  - **Read again**, which asks the model again;
  - **Replace photo**, which uploads a better photo as a new receipt and deletes this one once the photo is safe;
  - **Delete**, which deletes the receipt and its lines, to the recycle bin.

  **Open in PaperGo** corrects its values by hand; choosing **Read** as its status there takes it out of review, though without lines.

The figures are worked out by the server ([aggregates](../../docs/application-foundation.md)) and follow new and read receipts live.

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
      batch: 20
```

The `shared` queue is installation-wide, so other workflows can batch with these receipts. A run waits for its answer without holding a worker. A request that is still open after `AI_BATCH_DEADLINE` (default 72h) fails, which marks the receipt **Needs review**. Repeating the workflow on a receipt that was read reuses the cached answer at once.

The sample has its names in Spanish too, in [`locales/es.json`](locales/es.json): a Spanish organization gets them, and `--language es` installs them anywhere.
