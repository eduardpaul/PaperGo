# Receipts package

A library of receipt photos, a list of the products on them, and a workflow that reads each photo. Install it by importing the included workspace package. PaperGo has no receipt routes, tables, or upload-time processing: the workflow is an ordinary TypeScript script, [`read-receipts.ts`](read-receipts.ts), and you can open and change it in the workflow editor.

The workflow starts when a JPEG, PNG, or WebP file is uploaded, restored, or saved over WebDAV, and when you start it by hand. A newer file for the same item replaces the run that is still going (`concurrency: "replace"`). It asks for a [smaller copy](../../docs/documents.md#image-optimization) and promotes one when the image is suitable; an animated image, or any other image the optimizer keeps, continues with the file that was uploaded. A [batch completion](../../docs/ai.md#batches) then reads that image, with a schema for the store, purchase date, currency, total, and product lines. Amounts are exact decimal strings. The workflow writes the receipt's values at the version it read, so a newer file or edit is not overwritten, replaces any lines it filed earlier, and links each new line with **contains receipt line** (one parent receipt per line). It marks the receipt **Read** only after those lines are stored. A refused answer, a line that cannot be stored or a changed receipt marks it **Needs review**. After a promoted copy is filed, it asks to reclaim the replaced photo once the grace period ends.

## Install

1. As an organization administrator, open **Administration → Export and import**.
2. Select [`receipts.zip`](receipts.zip), choose **Try it**, then **Import**.
3. Open **Receipts desk** from the result and upload receipt photos to **Receipts**.
4. Find the extracted products in **Receipt lines**, linked to their receipt.

The ZIP includes the empty library and list, content types, fields, saved views,
relationship policy and the **Read receipts** workflow, its parameters pointing at
the imported library, list and line content type. No script, token, ID editing or
pre-existing workspace is needed. To install another independent copy, enter a
different workspace name when importing. Import does not call the model.

The importer becomes the workflow's author and the workspace's owner. Runs use
that author's permissions. `resources.json`, `read-receipts.ts` and
`read-receipts.json` are the editable sources for the package; the
[sample package test](../../internal/httpapi/samplepackages_test.go) checks the ZIP
through the import API and can rebuild it during development, and the
[receipts test](../../internal/scriptrun/receipts_test.go) runs it end to end.

## Batch execution

The script asks for `execution: "batch"` and queue `shared`. `AI_EXECUTION` can stay `immediate`; that deployment default applies only when a request leaves execution empty. Batch mode does not fall back to the chat provider. Configure the batch endpoint on its own:

```bash
AI_BATCH_PROVIDER=openai
AI_BATCH_MODEL=...
AI_BATCH_ENDPOINT=...          # optional; defaults to the chat endpoint
AI_BATCH_API_KEY_FILE=...      # when the endpoint requires a key
```

The `ai-batch` worker sends a full group of 20 distinct requests at once, and whatever waits at 01:00 UTC. One receipt waits for that flush unless other requests fill a group of 20. Change the group size, a maximum wait or the flush schedule in the `workers.ai-batch` section of `config.yaml`, or send what waits now with `papergo-admin workers flush ai-batch`. The `shared` queue is installation-wide, so other workflows can batch with these receipts. A run waits for its answer without holding a worker. A request that is still open after `AI_BATCH_DEADLINE` (default 72h) fails, which marks the receipt **Needs review**. Repeating the workflow on a receipt reuses the cached answer at once.

Do not add source `workflow` to the file triggers. Promoting the smaller copy is itself a file change, and that source would replace the run that is still reading the receipt.
