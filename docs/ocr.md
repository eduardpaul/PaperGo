# OCR

PaperGo reads text in page images through an OCR layer:

- **`internal/ocr`** is the port. An `Engine` detects text regions (`Detect`) and reads text lines with their positions (`Recognize`) in a decoded image.
- **`internal/ocr/paddle`** is the only engine: PaddleOCR running in ONNX Runtime.

Engines never read storage or the database. Callers decode the file, apply EXIF orientation and pass an `image.Image` of at most 64 million pixels (`ocr.MaxPixels`). Workflow activities use the layer through the port only (see [workflows](workflows.md#building-features-on-workflows)).

Workflow activities use it: [`file.optimize_image`](workflows.md#image-optimization) detects text to shrink images, [`file.compose_pdf`](workflows.md#photos-to-pdf) also recognizes it for the text layer of searchable PDFs (`internal/pdf`), and [`document.read_text`](workflows.md#reading-text) reads scanned PDF pages and photos, keeping their text for search.

## Configuration

Standalone executables and the Docker image include ONNX Runtime and PaddleOCR models and enable OCR automatically. Source builds (`go run ./cmd/api`) use the following environment settings:

| Setting | Meaning |
| --- | --- |
| `ONNXRUNTIME_LIB` | Path of the ONNX Runtime shared library (pinned version 1.23.2). Standalone builds select their bundled library when unset. In source builds, empty turns OCR off; activities that need it fail with a message saying so. |
| `OCR_MODELS` | Directory of the [model files](../models/paddleocr/README.md), defaulting to `assets/models/paddleocr` beside the executable. The native development environment selects the source `models/paddleocr` directory. When the default directory is absent, OCR stays off and the server starts. An explicit directory must exist. A present directory is checked at startup and loaded on first use. |
| `OCR_THREADS` | CPU threads one inference uses, 1 to 32 (default 1). |

## Results

| Call | Returns |
| --- | --- |
| `Detect(ctx, img, DetectOptions{MinScore, MaxDimension})` | `Detection`: the regions in reading order, each a `Quad` with the detector's `Score` (0 to 1). Also the size of the preview the detector analyzed, because regions only a few preview pixels high are unreliable. `MaxDimension` (256 to 2600) bounds only the preview's longest side instead of the engine's default, so a caller can analyze a larger preview, as image optimization does. |
| `Recognize(ctx, img)` | `Page`: its `Lines` in reading order, each with `Text`, `Confidence` (the mean of its characters), `Quad` and `Words`, each word with its own `Quad`. `Page.Text()` joins the lines. An image without text gives a page without lines. |
| `Info()` | the engine's name and its model files with their SHA-256, for run metrics |

Coordinates:

- A `Quad` holds four corners in pixels from the image's top-left corner, in the text's reading order: top left, top right, bottom right, bottom left. A line on an upside-down page therefore starts at the bottom right.
- `Quad.Height()` is the shortest side: the height of the text line, also when the text is rotated.

Scripts without spaces (Chinese, Japanese, Korean) have one word per character.

## PaddleOCR engine

The engine runs the same models, preprocessing and thresholds as PaperDotNet, so both applications read text the same way. The models are in [`models/paddleocr`](../models/paddleocr/README.md):

1. **Detection.** PP-OCRv6 small DBNet runs on a bounded preview:
   - The preview's longest side is at most `MaxDetectionDimension` (default 1536), and smaller for square images, so it stays near one megapixel. With `DetectOptions.MaxDimension`, only the longest side is bounded. An image already small enough is analyzed as it is.
   - Its sides are rounded to multiples of 32. Pixels are BGR with ImageNet mean and standard deviation; transparency is flattened onto white.
   - The probability map is thresholded at 0.2 and dilated by one pixel. Each region becomes its minimum-area rectangle, scored by the mean probability inside it.
   - Rectangles with a short side under 3 pixels are dropped. The rest are expanded by area × 1.4 / perimeter, and dropped if their short side is then under 5 pixels.
   - Regions are projected to the source image and sorted top to bottom. Regions whose tops are within 10 preview pixels share a line, which reads left to right.
2. **Rectification.** Each region is cut out of the original pixels, never the preview, and straightened with a perspective transform. A region at least 1.5 times taller than wide is vertical text and is turned a quarter turn clockwise.
3. **Orientation.** The PP-LCNet classifier sees the line on a 160×80 mid-gray canvas. The line is turned over when the classifier is at least 90% sure it is upside down.
4. **Recognition.** PP-OCRv6 small reads the line at 48 pixels high:
   - Greedy CTC decoding drops blanks and repeated classes, and keeps each character's confidence and column.
   - `Recognize` detects with a minimum score of 0.5. It drops lines whose characters average less than 0.5, and lines of only spaces.
   - Words span from half a column before their first character to one and a half after their last.

Inference is serialized across the whole process, so OCR uses a bounded amount of CPU and native memory however many workflow runs ask for it. A cancelled context stops a waiting call, or terminates a running inference.

Limits:

- An image above 64 megapixels is refused.
- A text line above 16 megapixels, or longer than 128 times its height, fails the call; crop or split such pages first.

### Runtime

`internal/onnx` uses [`yalue/onnxruntime_go` v1.25.0](https://github.com/yalue/onnxruntime_go/tree/v1.25.0), a cgo binding for ONNX Runtime 1.23.2. OCR and local embeddings share one environment. Models load from memory or a path, tensors use the SDK, and context cancellation terminates only the active run. CPU arenas and memory patterns are disabled to bound retained memory for varying image shapes.

PaperGo builds with cgo. `python3 scripts/native.py` installs the pinned SDKs and writes `.tools/native/env.sh` and `env.ps1`. The Docker image and release archives include ONNX Runtime, PDFium and zvec. PDF reading is always available; ONNX Runtime inference is enabled with `ONNXRUNTIME_LIB` and the model paths. The Docker image enables OCR and includes its models. Native license notices are under `/opt/papergo/notices` in Docker and `notices/` in platform ZIPs. Shared models and their notices are in `assets/` beside the executable.

Configuration (`paddle.Config`):

| Field | Meaning |
| --- | --- |
| `Library` | path of the ONNX Runtime shared library |
| `Models` | directory of the model files |
| `Threads` | CPU threads per inference, 1 to 32 (default 1) |
| `MaxDetectionDimension` | longest side of the detection preview, a multiple of 32 from 256 to 2600 (default 1536) |

`New` checks the configuration and hashes the model files. The library and the models load on first use. After five minutes without detection or recognition, the engine releases its model sessions; the next call reloads them. Running inference holds off eviction. `Close` releases the models immediately.

### Performance

Measured on a 4-core x86-64 container with one thread:

- **A 960×960 detection:** about 330 ms.
- **One 48×320 text line:** about 21 ms. Wider lines take longer.
- **An A4 page of 45 lines (1240×1754):** 5.7 s with one thread, 3.9 s with four.

Inference dominates; image preparation in Go takes about 15% of the time.

## Trying it

`cmd/ocr` runs the engine on JPEG, PNG or WebP files and prints JSON. It does not apply EXIF orientation.

```bash
python3 scripts/native.py
source .tools/native/env.sh
go run ./cmd/ocr receipt.jpg              # lines, words and text
go run ./cmd/ocr -detect -min-score 0.5 receipt.jpg
```

## Tests

`internal/ocr/paddle` has two kinds of tests:

- **Unit tests** cover the post-processing geometry, CTC decoding, word placement and rectification.
- **Engine tests** render text with the Go fonts and check what the real models read. They cover upside-down and transparent pages, blank pages, concurrent use and cancellation.

The engine tests need `ONNXRUNTIME_LIB`. They are skipped without it, unless `PAPERGO_REQUIRE_OCR` is set. CI installs the pinned release and requires them.

The ONNX Runtime binding lives in `internal/onnx` and is shared with the [local embedding engine](embeddings.md). Both engines use one process-wide library and environment; configure the same library path for both.
