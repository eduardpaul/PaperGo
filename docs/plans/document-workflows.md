# Document workflows: storage optimization and photo to document (plan)

Status: **proposed.** Nothing in this plan is implemented yet. It reviews what PaperGo workflows can do today, lists what the engine still needs, and plans how two PaperDotNet workflows move to PaperGo as built-in workflows:

- **Optimize document storage**: find the smallest reliable text height in a photo, rescale the photo so that text stays readable, and compress it. A manager reviews the result before it replaces the original.
- **Photo to document**: combine several selected photos, in order, into one searchable PDF. A manager reviews the PDF before it replaces the primary photo and the other photos are deleted.

The PaperDotNet sources were reviewed at `main` `96df412` (storage optimization, [ADR-0046](https://github.com/eduardpaul/PaperDotNet/blob/96df412f040f53cc14711aaba35880e24817e084/docs/adr/0046-reviewed-document-storage-optimization.md)) and at the `feature/multi-item-workflow-photo-to-document` branch `a0894cb` (ADR-0047 photo composition, ADR-0048 shared OCR module, `docs/ocr.md`). PaperDotNet is a reference only. The plan ports behaviour and contracts, not .NET structure.

## Principles

These two rules decide every choice below. PaperGo already states both in [workflows](../workflows.md) and the [runner plan](runner.md).

1. **Everything that happens after an item is added or changed is a workflow.** The upload stores the file and logs its event, and does nothing more. Text, thumbnails, OCR, optimization and composition are activities used by built-in workflows. They are never code in the upload path.
2. **Workspace managers can see, change, turn off or replace every workflow.** That includes the built-ins in this plan. A built-in is set through its parameters. To change its flow, a manager copies it into an ordinary workflow (`POST .../workflow-builtins/{key}/copy`). Neither built-in in this plan is a locked system role.

A third rule follows from the second: **build features from workflow parts.** An approval, a "promote this staged file" step and a "delete these items" step are general activities. The photo workflow reuses the optimization workflow's parts instead of shipping a second set.

## Review: what PaperGo workflows have today

Phase 1 of the [runner](runner.md) is done. The pieces both workflows need are compared below.

| Capability | PaperGo today | Needed by | Gap |
| --- | --- | --- | --- |
| Item triggers with collection and content type filters | `item.created`, `item.updated`, … with `blob_id` in the data | optimization | No file-specific event. A workflow that replaces a file raises `item.updated` and cannot tell its own replacement apart from a new upload. |
| Manual triggers over an ordered selection | `selection: "selection"`, `{run:items}`, primary item, cancellation when a member is deleted (except by the run itself) | photo to document | None. This was ported from PaperDotNet PR #8. Only the `x-papergo-selection` hints need an `image` preview in the web UI. |
| Launch forms and built-in parameters | JSON Schema forms with domain pickers (`people`, `item`, `terms`, …) | both | None |
| Durable waits | `delay` (DBOS `Sleep`) | both (review) | **No approval.** The runner plan lists it for phase 3 (DBOS `Recv`). |
| Concurrency per item | none | optimization (`replace`), photo (`skip`) | Missing. Listed for phase 3. |
| Loops | node loops through ports; no `forEach` | photo (delete the other members) | `forEach` or a `script` node |
| Reading and writing data in a node | item activities through `dms` as the version's author | both | **No file access.** Activities cannot read a file version, stage new bytes, or promote staged bytes as a new file version. |
| Heavy work in a node | every node is **one SQLite write transaction** (`internal/runner/steps.go`, `stepTx`) | both | **Blocking.** Decoding, OCR and encoding take seconds to minutes. Inside `stepTx` they would hold the only SQLite writer that whole time. |
| Data logic | tokens only | parity with PaperDotNet | No `script` node (PaperDotNet ADR-0037, Jint) |
| Document processing | none: no text extraction, thumbnails, page images or OCR. The upload stores only the file and its OCR language hint. | both | Everything: image pipeline, text detection, OCR, PDF writer |
| Web editor | visual flow editor, built-in settings and copies, schema forms, manual and selection launches | both | Approval inbox and review screens (image comparison, PDF preview) |

### Findings on the architecture

- **The workflow model is right for these ports.** Definitions are data with immutable versions. Runs act as the version's author, nodes are exactly-once steps, built-ins are copyable, and selection runs exist. Both PaperDotNet workflows map onto this model without new concepts except the ones listed below.
- **One node is one write transaction, and that is the main constraint.** It gives exactly-once writes cheaply, and it should stay the rule for writes. CPU-heavy work needs a second kind of step that runs outside the transaction (see [work steps](#1-work-steps-heavy-work-outside-the-write-transaction)).
- **The storage collector would delete staged files.** The collector deletes unreferenced objects older than one hour. A candidate waiting days for review is unreferenced by `blobs`, so it would be deleted. Staged files need their own references.
- **File history is immutable.** PaperDotNet frees the original's bytes once an optimization is accepted, as an explicit exception to keeping every file version (ADR-0046). In PaperGo, item revisions pin their blob, and revisions cannot be updated or deleted. A "reclaim the original" option is therefore a schema decision (see [decisions](#decisions-needed)). Without it, an accepted optimization makes the item's current file smaller but frees no disk space until the original file version leaves history.
- **The CGO-free, `FROM scratch` image limits OCR.** PaperDotNet runs PaddleOCR ONNX models in-process. In Go that needs `onnxruntime_go` (MIT), which requires CGO and a native shared library. Tesseract is an external executable. Either way, the current single static binary cannot run OCR (see [the image and OCR stack](#image-ocr-and-pdf-stack-in-go)).

## Engine additions

These are general workflow parts. Each is useful on its own, and they fit phase 3 of the runner plan.

### 1. Work steps: heavy work outside the write transaction

Add `Exec.Work(name, fn)` beside `Exec.Step`. It runs `fn` as a DBOS step **without** a PaperGo transaction, and DBOS records its JSON output (`dbos.RunAsStep`). An activity that needs heavy work runs in two parts:

1. **work**: read inputs (file versions by object key), compute, write any output bytes to the store as a *staged file*, and return small metadata (staged file ID, metrics).
2. **commit**: an ordinary `stepTx` that checks freshness and records the result.

Rules:

- Work is keyed by the run ID and the node's execution, so a retry or a crash before DBOS records the output produces the same staged file again, or reuses it if it exists. Work is safe to repeat. It never writes domain rows.
- A process-wide **processing gate** bounds concurrent image and OCR work (default 1), as `ImageOptimizationGate` and `PhotoCompositionGate` do in PaperDotNet. The `workflows` queue keeps 4 concurrent runs per node, so the other runs can continue while one waits on the gate. The runner plan's separate `system` queue can take this work later.
- Limits are configuration: maximum decoded pixels (default 64 MP), OCR timeout, analysis dimension. Exceeding one fails the node and keeps the source.

### 2. Staged files

A new table, `staged_files (id, workspace_id, run_id, node, execution, object_key, filename, content_type, size, sha256, metrics, state, created_at)`, where `state` is `pending`, `promoted` or `discarded`.

- The storage collector treats an object as referenced while a `pending` staged file holds it.
- When a run finishes or is cancelled, its pending staged files are discarded, and the collector removes their bytes after its normal grace period. Purging an item discards the staged files whose source is that item.
- `dms` gets three methods, all checked as the run's author:
  - `OpenFileVersion(item, version)` reads the bytes of a file version.
  - `StageFile(...)` records a staged object.
  - `PromoteStagedFile(item, staged, sourceVersion)` attaches the staged bytes as a new file version, **only if the item's current file is still `sourceVersion`**. Otherwise it fails with a conflict, so a stale candidate never replaces a newer upload. It uses the same revision, audit and event path as `PUT /v1/items/{id}/content`, with `source: "workflow"`.

### 3. A `file.added` trigger

Add a domain event, `file.added`, raised when a new file version becomes an item's current file:

- **data**: `source` (`upload`, `webdav`, `restore`, `workflow`, `import`), `file_version`, `blob_id`, `content_type`, `size`, `revision_id`
- **trigger filters**: `collection_id`, `content_type_id`, and `sources` (default: everything except `workflow`)

Optimization listens to uploads, WebDAV, imports and restores, but not to its own promotion. Without the source filter, accepting a candidate would raise a new event, which would start the workflow again. The depth limit would stop that loop, but only after wasted runs. The optimization activity also skips a run whose `file_version` is no longer current, so a superseded upload is never optimized.

### 4. Approval with reviews

An `approval` activity:

- **inputs**: `title`, `assignees` (people picker values, defaulting to workspace managers), an optional `input_schema` form for the decision, and an optional `review` (`{"type": "file.compare" | "file.preview", "staged_file_id": "..."}`)
- **ports**: `approved` and `rejected`. Output: `decision`, `decided_by`, `input`.

It is built on DBOS `Recv` with the run ID, as the runner plan describes. The decision endpoint checks that the decider is assigned and can read the item, that the review is still fresh (the source is still current), and the decision form. It then calls `Send` in its own transaction with an idempotency key. An `approval_requests` table lists open requests for the inbox.

Review types are presentation contracts that the web UI renders:

- `file.compare`: original and candidate side by side at 100 % (one image pixel per CSS pixel), with the metrics
- `file.preview`: the staged PDF with its page order

The server never trusts the renderer. It checks freshness again when the decision commits.

### 5. Concurrency policies

Add `"concurrency": "parallel" | "skip" | "replace"` to definitions:

- It is checked at dispatch and again when the run starts.
- Selection runs compare every member, from the run's recorded `items`.
- `replace` cancels the earlier run, and cancelling discards its staged files.
- Optimization uses `replace`, so a new upload cancels the review of an obsolete candidate. Photo to document uses `skip`.

### 6. `forEach`

`forEach` iterates `items` (an array or a token such as `{run:items}`) with `as` and a body start node, and resets nested loops on each outer element (the fix from PaperDotNet ADR-0037). Photo to document uses it to delete the other members. The [script node](#script-node-on-goja) can do the same in one step.

## The two built-ins

### Optimize document storage (`documents.optimize`)

Per library, off by default.

```json
{
  "triggers": [
    {"type": "file.added", "collection_id": "{param:collection}", "sources": ["upload", "webdav", "import", "restore"]},
    {"type": "manual", "collection_id": "{param:collection}"}
  ],
  "concurrency": "replace",
  "flow": {
    "start": "prepare",
    "nodes": {
      "prepare": {"activity": "file.optimize_image", "inputs": {
        "target_height": "{param:target_height}", "min_confidence": "{param:min_confidence}", "percentile": "{param:percentile}",
        "max_analysis_dimension": "{param:max_analysis_dimension}", "minimum_scale": "{param:minimum_scale}", "quality": "{param:quality}"
      }, "next": {"candidate": "review", "skipped": "done"}},
      "review": {"activity": "approval", "inputs": {
        "title": "Review smaller file for {item:name}", "assignees": "{param:approvers}",
        "review": {"type": "file.compare", "staged_file_id": "{step:prepare.staged_file_id}"}
      }, "next": {"approved": "accept", "rejected": "discard"}},
      "accept": {"activity": "file.promote", "inputs": {"staged_file_id": "{step:prepare.staged_file_id}", "source_version": "{step:prepare.source_version}"}},
      "discard": {"activity": "file.discard", "inputs": {"staged_file_id": "{step:prepare.staged_file_id}"}},
      "done": {"activity": "end"}
    }
  }
}
```

Parameters use the same names and defaults as PaperDotNet, in PaperGo's snake case:

| Parameter | Default | Range |
| --- | --- | --- |
| `target_height` | 12 px | 4 to 200 |
| `min_confidence` | 50 | 0 to 100 |
| `percentile` | 5 | 0 to 100 |
| `max_analysis_dimension` | 2600 | 256 to 10000 |
| `minimum_scale` | 0.05 | greater than 0, at most 1 |
| `quality` | 80 (WebP) | 1 to 100 |
| `approvers` | workspace managers | people picker |

`file.optimize_image` is a work step plus a commit step. It ports `ImageOptimizationAdapter`:

1. Skip, through the `skipped` port with a `reason`, when any of these holds:
   - the format is not static JPEG, PNG or WebP (animated WebP and APNG are detected, and APNG through its `acTL` chunk before `IDAT`);
   - the file came from a workflow;
   - `file_version` is no longer current;
   - an earlier run already recorded a choice for this file version.
2. Decode within the pixel limit, then apply the EXIF orientation.
3. Make an analysis copy whose longest side is at most `max_analysis_dimension`, with scale factor `k`.
4. Detect text and collect text heights projected back to source pixels:
   - line strategy (detector): the shortest side of each oriented box, keeping boxes with confidence ≥ `min_confidence`, an analysis height ≥ 6 px and an aspect ratio ≥ 1.5;
   - word strategy (Tesseract TSV): level-5 words with confidence ≥ `min_confidence` and at least 2 letters or digits; each height is `round(height / k)`, kept when it is above 3.
5. Find the smallest reliable height: the interpolated `percentile` of the heights, at least 4 px. No heights means `skipped` ("no reliable text").
6. Compute `scale = clamp(target_height / smallest, minimum_scale, 1)`. The image is never upscaled.
7. Resize with a bicubic filter and encode WebP at `quality`. If the result is not smaller, skip ("not smaller").
8. Stage the candidate and output `staged_file_id`, `source_version` and the metrics PaperDotNet records:
   - `detector`, `strategy`, `text_regions`, `analysis_ms`, `analysis_scale`
   - `source_width`, `source_height`, `width`, `height`, `source_bytes`, `bytes`, `scale`, `smallest_text_height`, `savings_percent`

`Percentile`, `ScaleForHeight`, `WordHeights` and `IsAnimatedPng` are pure functions. Port them with PaperDotNet's unit-test cases as Go table tests, so both implementations give the same numbers.

### Photo to document (`documents.photos_to_pdf`)

Per library, off by default, and started manually on a selection.

```json
{
  "input_schema": {"type": "object", "properties": {}, "x-papergo-selection": {
    "preview": "image", "item_label": "page", "order_label": "Page order",
    "primary_description": "This item gets the approved PDF as its new file. The other photos are deleted. Its metadata and file history are kept."
  }},
  "triggers": [{"type": "manual", "collection_id": "{param:collection}", "selection": "selection"}],
  "concurrency": "skip",
  "flow": {
    "start": "compose",
    "nodes": {
      "compose": {"activity": "file.compose_pdf", "inputs": {"target_height": "{param:target_height}", "quality": "{param:quality}", "languages": "{param:languages}"}, "next": {"done": "review"}},
      "review": {"activity": "approval", "inputs": {
        "title": "Review combined PDF for {item:name}", "assignees": "{param:approvers}",
        "review": {"type": "file.preview", "staged_file_id": "{step:compose.staged_file_id}"}
      }, "next": {"approved": "accept", "rejected": "discard"}},
      "accept": {"activity": "file.promote", "inputs": {"staged_file_id": "{step:compose.staged_file_id}", "source_version": "{step:compose.source_version}"}, "next": {"done": "others"}},
      "others": {"activity": "forEach", "inputs": {"items": "{step:compose.others}", "as": "member"}, "next": {"body": "recycle"}},
      "recycle": {"activity": "item.delete", "inputs": {"item_id": "{var:member}"}},
      "discard": {"activity": "file.discard", "inputs": {"staged_file_id": "{step:compose.staged_file_id}"}}
    }
  }
}
```

`file.compose_pdf` is a work step plus a commit step. It ports `ComposePhotosActivity`:

1. **Snapshot.** Record each member's current file version and lock version:
   - every member must be a writable static JPEG, PNG or WebP in the trigger's collection;
   - the snapshot is the freshness contract for review and promotion;
   - a changed member fails the run ("launch a new conversion"), so a stale selection is never regenerated silently.
2. **Normalize each page.** Apply the EXIF orientation and flatten transparency onto white.
3. **OCR at full resolution.** Detect the text, then compute the page's PDF image size so that the smallest reliable text is `target_height` (default **32 px**, as in PaperDotNet). Blank pages keep their size and their place in the order.
4. **Write the PDF.** Each page embeds the resized image as JPEG at `quality` (default 80), or as lossless Flate when that is smaller. Invisible text is placed at the recognized positions, with coordinates scaled to the resized image.
5. **Stage the PDF.** Output `staged_file_id`, `source_version` (the primary's file version), `others` (the other members in order), `pages` and `bytes`.

`file.promote` gives the primary item the PDF as a new file version. Its metadata and earlier files stay in its history. `item.delete` then sends the other members to the recycle bin, where they stay recoverable. The run is the cause of those deletions, so it is not cancelled (the existing selection-run rule).

One difference from PaperDotNet: there, promotion and recycling commit in one transaction. Here they are separate nodes, each exactly-once, so a crash between them resumes at the next node rather than rolling back. If that atomicity matters, `file.promote` can take an optional `delete_items` input and do both in one step.

## Image, OCR and PDF stack in Go

All new direct dependencies must be added to [dependencies](../dependencies.md).

| Need | Option | Licence | CGO | Notes |
| --- | --- | --- | --- | --- |
| Decode JPEG and PNG | standard library | BSD-3-Clause | no | |
| Decode WebP, resample | `golang.org/x/image` (`webp`, `draw.CatmullRom`) | BSD-3-Clause | no | `x/image/webp` decodes only, and has no animation API. Detect `ANIM`/`ANMF` chunks before decoding. |
| Encode WebP with a quality setting | `github.com/gen2brain/webp` | MIT (libwebp BSD-3-Clause, run by `tetratelabs/wazero` Apache-2.0) | no | libwebp compiled to WASM and run in-process. It is the only CGO-free lossy WebP encoder found. |
| EXIF orientation | `github.com/rwcarlsen/goexif` | BSD-2-Clause | no | Unmaintained since 2019. Alternatively, a small parser for tag 0x0112 only, with fuzz tests. |
| Word heights and recognition | Tesseract executable (TSV, hOCR) | Apache-2.0 | no (separate process) | PaperDotNet's alternative strategy. It needs `tesseract` and traineddata in the image. |
| Text region detection like PaperDotNet's default | PaddleOCR PP-OCRv6 DBNet ONNX through `github.com/yalue/onnxruntime_go` | MIT, models Apache-2.0, ONNX Runtime MIT (with an Eigen MPL-2.0 part) | **yes**, plus `libonnxruntime.so` | Breaks the CGO-free static build. As a separate OCR sidecar, it keeps the API binary unchanged. |
| Write PDFs with an image and invisible text | `github.com/pdfcpu/pdfcpu` (Apache-2.0) or `github.com/signintech/gopdf` (MIT) | | no | To be confirmed in a spike: JPEG and Flate images, text render mode 3 and a Unicode font subset. |

**Recommendation.**

- Do the image work in pure Go: standard library, `x/image` and `gen2brain/webp`.
- Put OCR behind an `internal/ocr` port with two operations:
  - `DetectText(image) → heights + engine metrics`
  - `Recognize(pages) → words with boxes per page`
- Ship one engine first, **Tesseract**, run as a process with `OMP_THREAD_LIMIT=1`. It covers both workflows: the word strategy for optimization, and hOCR or TSV words for the PDF text layer.
- Publish two images:
  - the current `scratch` image, without OCR, where the OCR activities fail with "OCR is not available in this deployment";
  - a Debian-slim image with `tesseract-ocr` and a few language packs.
- A Paddle sidecar can come later behind the same port, for parity with PaperDotNet's default detector.

PaperDotNet's notes apply to every engine. Word heights and line boxes are not the same measurement, so the same `target_height` gives different scales per strategy. Both strategies need fresh benchmarks on PaperGo hardware, and a manager review stays mandatory.

## Script node on goja

PaperDotNet's `script` activity (ADR-0037) runs sandboxed JavaScript for data work, with a contract shared with its TypeScript SDK. Neither ported workflow needs it: `forEach` and the generic activities cover them. A `script` node is still the right PaperGo answer to the same problem, data reshaping without one activity per need, and [goja](https://github.com/dop251/goja) is the fitting engine:

- pure Go, so the build stays CGO-free;
- MIT, and it links `dlclark/regexp2` (MIT), `go-sourcemap/sourcemap` (BSD-2-Clause), `google/pprof` (Apache-2.0) and `golang.org/x/text`;
- goja has no tagged releases, so pin a pseudo-version.

A prototype on goja `v0.0.0-20261013122152-245c54f48cfc` checked these:

| Feature | Result |
| --- | --- |
| strict mode, `async`/`await` over Go-resolved promises (`vm.NewPromise`) | works |
| destructuring, rest, `Array.prototype.at`, `Object.groupBy`, `BigInt` | works |
| time limit through `vm.Interrupt` from a timer | stops `for(;;){}` |
| call-depth limit | `SetMaxCallStackSize` |
| memory limit | **none**: goja has no allocation limit, unlike Jint's 32 MB |

### Contract (parity with PaperDotNet)

Keep PaperDotNet's script names and shapes, so scripts and knowledge carry over. Adapt the parts PaperGo models differently:

| PaperDotNet (Jint) | PaperGo (goja) |
| --- | --- |
| `item`, `vars` (changes kept), `steps`, `trigger`, `log(text)` | the same; `item` has PaperGo's item shape, as in `{item:...}` |
| `items.get/query/create/update/delete(list, …)`, with lists by name | the same functions with a collection ID, `query(collection, {filter, sort, limit})` in the PaperGo filter language; also `run.items` for selections |
| writes are **planned**, saved, then applied one by one (not atomic, continued after a crash) | writes go through `dms` **inside the node's step transaction**: all or nothing, and exactly once through the step marker, so no plan is needed |
| limits: 2 s, 32 MB, 1,000,000 statements, depth 100, 200 reads, 1000 writes, 50,000 characters | 1 s (the step holds the SQLite writer), depth 100, 200 reads, 1000 writes, 50,000 characters, inputs and output within the 64 KB node limit; no memory or statement limit |
| ports `done` and `error`; output `result`, `created`, `updated`, `deleted` | the same |

Writing inside the transaction is simpler and stronger than plan-then-apply. The cost is that the script holds the single writer while it runs, which is why the time limit is shorter. The missing memory limit is a known gap. Two mitigations:

- the time limit, together with input and output caps, bounds what a script can allocate in practice;
- scripts are written by workspace managers, who already have manage rights.

Parity tests: port the cases of PaperDotNet's `sdk/typescript/test/scripts.test.mjs` as JSON fixtures (script, inputs, expected result, variables, log, writes and errors), and run them against the goja implementation in `internal/workflow`. The editor gets TypeScript declarations of the globals, as `scriptDeclarations` does in PaperDotNet.

## Phases

Each phase ships with its tests, OpenAPI contract and docs, and changes `migrations/20261007000100_schema.sql` in place ([AGENTS.md](../../AGENTS.md)).

1. **Engine parts:**
   - work steps and the processing gate;
   - `staged_files` and collector references;
   - the `file.added` event and trigger;
   - concurrency policies;
   - `forEach`;
   - `approval` with `approval_requests`, the decision endpoint and a basic approvals inbox in the web UI.

   Tests:
   - a crash during a work step, then a rerun that reuses the staged file;
   - a stale promotion is refused;
   - `replace` cancels a pending review and discards its staged file;
   - the collector keeps pending staged files.
2. **Optimization:**
   - the image pipeline in pure Go;
   - the `internal/ocr` port with the Tesseract engine and the OCR image;
   - `file.optimize_image`, `file.promote` and `file.discard`;
   - the `documents.optimize` built-in;
   - the `file.compare` review screen.

   Tests: PaperDotNet's numeric cases, synthetic images with known text sizes, and end-to-end runs with approve and reject.
3. **Photo to document:**
   - Tesseract recognition with word boxes;
   - the PDF writer (after the library spike);
   - `file.compose_pdf` and the `documents.photos_to_pdf` built-in;
   - `image` previews in the selection launch screen;
   - the `file.preview` review screen.

   Tests:
   - page order, blank pages and a changed member;
   - the deleted members are recoverable;
   - the PDF text layer can be extracted.
4. **Script node on goja.** This phase is independent of 2 and 3 and can run in parallel with them after phase 1. It covers the contract, limits, parity fixtures and editor declarations.

**Follow-up**, outside these two ports and needed to complete the first principle: the baseline document built-ins PaperDotNet ships for every library (read the text, thumbnails, page images, OCR after "no text"). They use the same work steps and OCR port.

## Decisions needed

1. **OCR deployment.** The options are:
   - **(a)** two images: `scratch` without OCR, and Debian-slim with Tesseract (recommended);
   - **(b)** one Debian-slim image for everyone;
   - **(c)** an OCR sidecar (Tesseract or Paddle) behind HTTP, keeping the API image `scratch`.
2. **Reclaiming originals after an accepted optimization.** The options are:
   - keep the original as an earlier file version (no schema change; frees no disk space until it leaves history);
   - add a "reclaimed" file-version state that keeps the metadata, hash and metrics but frees the bytes. This is PaperDotNet's behaviour. It is an explicit exception to immutable file history and needs a schema change.
3. **Paddle parity.** Decide whether PaperDotNet's default DBNet line detector is required, or whether the Tesseract word strategy is acceptable at first. The two strategies measure different things, so default parameters may need tuning.
4. **Script writes.** Decide between direct writes in the step transaction (recommended, atomic) and PaperDotNet's plan-then-apply model (identical semantics, more machinery).
