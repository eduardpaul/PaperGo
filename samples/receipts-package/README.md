# Receipts package

A library of receipt photos, a list of the products on them, and a workflow that reads each photo. Install it by importing the included workspace package. PaperGo has no receipt routes, tables, or upload-time processing: the workflow is ordinary configuration, and you can open and save it with the workflow API.

The workflow starts when a JPEG, PNG, or WebP file is uploaded, restored, or saved over WebDAV, and when you start it by hand. A newer file for the same item replaces the run that is still going. It asks `file.optimize_image` for a smaller copy and promotes one when the image is suitable. An animated image, or any other image the optimizer keeps, continues with the file that was uploaded. `ai.complete` then reads that image in batch mode, with a schema for the store, purchase date, currency, total, and product lines. Amounts are exact decimal strings. The workflow checks that the same file is still current, replaces any lines it filed earlier, and links each new line with **contains receipt line** (one parent receipt per line). It marks the receipt **Read** only after those lines are stored. An extraction or filing error marks it **Needs review**. After a promoted copy is filed, it asks `file.reclaim` to drop the replaced source once the grace period ends.

## Install

1. As an organization administrator, open **Administration → Export and import**.
2. Select [`receipts.zip`](receipts.zip), choose **Try it**, then **Import**.
3. Open **Receipts desk** from the result and upload receipt photos to **Receipts**.
4. Find the extracted products in **Receipt lines**, linked to their receipt.

The ZIP includes the empty library and list, content types, fields, saved views,
relationship policy, **Read receipts** workflow and enabled **AI batch coordinator**.
No script, token, ID editing or pre-existing workspace is needed. To install another independent copy, enter
a different workspace name when importing. Import does not call the model.

The importer becomes the workflow's author and the workspace's owner. Runs use
that author's permissions. Open **Read receipts** in the workflow editor to
customize it. `resources.json` and `workflow.json` are the editable sources for the
package; the [sample package test](../../internal/httpapi/samplepackages_test.go)
checks the ZIP through the import API and can rebuild it during development.

## Batch execution

The sample sets `execution: batch` and queue `shared` on its `ai.complete` step. `AI_EXECUTION` can stay `immediate`; that deployment default applies only when a step leaves execution empty. Batch mode does not fall back to the chat provider. Configure the batch endpoint on its own:

```bash
AI_BATCH_PROVIDER=openai
AI_BATCH_MODEL=...
AI_BATCH_ENDPOINT=...          # optional; defaults to the chat endpoint
AI_BATCH_API_KEY_FILE=...      # when the endpoint requires a key
```

The package enables **AI batch coordinator** in the imported workspace, with the importer as its author. No `AI_BATCH_WORKSPACE` setting is needed for this sample. Each coordinator run needs `organization.manage`; receipt runs use their own author’s content permissions. The `shared` queue is installation-wide, so other workflows can batch with these receipts. If an existing coordinator already manages that queue, turn off the imported one in the workspace’s workflow settings and use the existing coordinator’s policy.

With the coordinator's defaults, full groups of 20 distinct uncached requests are submitted on each pass, and a partial group is also submitted at 01:00 Europe/Madrid. One receipt waits for that flush unless other requests already fill a group of 20. Set `batch_size` (1–100) to change the threshold. Set `policy: schedule` for one daily admission window, `threshold` for size-only submission, or `both` for size plus cron. Configure these parameters on the `ai.batch` built-in; the receipt workflow stays the same. A daily backlog larger than `batch_size` drains over consecutive coordinator passes. The coordinator polls the provider every five minutes. A request that is still open after `AI_BATCH_DEADLINE` (default 72h) follows the step's error path, which marks the receipt **Needs review**.

Do not add source `workflow` to the file trigger. Promoting the smaller copy is itself a file change, and that source would cancel the run that is still reading the receipt.
