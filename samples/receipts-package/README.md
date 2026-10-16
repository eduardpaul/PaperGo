# Receipts package

A library of receipt photos, a list of the products on them, and a workflow that reads each photo. Installing it uses the public HTTP API only. PaperGo has no receipt routes, tables, or upload-time processing: the workflow is ordinary configuration, and you can open and save it with the workflow API.

The workflow starts when a JPEG, PNG, or WebP file is uploaded, restored, or saved over WebDAV, and when you start it by hand. A newer file for the same item replaces the run that is still going. It asks `file.optimize_image` for a smaller copy and promotes one when the image is suitable. An animated image, or any other image the optimizer keeps, continues with the file that was uploaded. `ai.complete` then reads that image in batch mode, with a schema for the store, purchase date, currency, total, and product lines. Amounts are exact decimal strings. The workflow checks that the same file is still current, replaces any lines it filed earlier, and links each new line with **contains receipt line** (one parent receipt per line). It marks the receipt **Read** only after those lines are stored. An extraction or filing error marks it **Needs review**. After a promoted copy is filed, it asks `file.reclaim` to drop the replaced source once the grace period ends.

## Install

The caller needs `contentType.manage` to create the fields and content types, and manage on the workspace. The workflow's author is that caller; later runs keep that author's permissions.

```bash
export PAPERGO_URL=http://127.0.0.1:8080
export PAPERGO_TOKEN=...
python3 samples/receipts-package/setup.py
```

`--workspace` (or `PAPERGO_WORKSPACE`) installs into a workspace that already exists. Otherwise the script creates **Receipts desk**. The script prints one JSON object with `workspace_id`, `library_id`, `lines_id`, and `workflow_id`. Running it again reuses what is already there and saves the workflow definition once more.

Edit the installed workflow with `GET /v1/workflows/{id}` and `PUT /v1/workflows/{id}` (`If-Match` from the GET). The definitions in this directory are the source the installer posts.

## Batch execution

The sample sets `execution: batch` and queue `shared` on its `ai.complete` step. `AI_EXECUTION` can stay `immediate`; that deployment default applies only when a step leaves execution empty. Batch mode does not fall back to the chat provider. Configure the batch endpoint on its own:

```bash
AI_BATCH_PROVIDER=openai
AI_BATCH_MODEL=...
AI_BATCH_ENDPOINT=...          # optional; defaults to the chat endpoint
AI_BATCH_API_KEY_FILE=...      # when the endpoint requires a key
AI_BATCH_WORKSPACE=...         # workspace that owns the coordinator
```

`AI_BATCH_WORKSPACE` is where PaperGo turns on the **AI batch coordinator**. The person who turns it on must manage that workspace, and each coordinator run needs `organization.manage`. The coordinator's queue is installation-wide. Receipt runs still file items as their own author.

With the coordinator's defaults, full groups of 20 distinct uncached requests are submitted on each pass, and a partial group is also submitted at 01:00 Europe/Madrid. One receipt waits for that flush unless other requests already fill a group of 20. Set `batch_size` (1–100) to change the threshold. Set `policy: schedule` for one daily admission window, `threshold` for size-only submission, or `both` for size plus cron. Configure these parameters on the `ai.batch` built-in; the receipt workflow stays the same. A daily backlog larger than `batch_size` drains over consecutive coordinator passes. The coordinator polls the provider every five minutes. A request that is still open after `AI_BATCH_DEADLINE` (default 72h) follows the step's error path, which marks the receipt **Needs review**.

Do not add source `workflow` to the file trigger. Promoting the smaller copy is itself a file change, and that source would cancel the run that is still reading the receipt.
