# OCR

PaperGo reads text in page images through an OCR layer:

- **`internal/ocr`** is the port. An `Engine` detects text regions (`Detect`) and reads text lines with their positions (`Recognize`) in a decoded image.
- **`internal/ocr/paddle`** is the only engine: PaddleOCR running in ONNX Runtime.

Engines never read storage or the database. Callers decode the file, apply EXIF orientation and pass an `image.Image` of at most 64 million pixels (`ocr.MaxPixels`). Workflow activities use the layer through the port only (see [workflows](workflows.md#building-features-on-workflows)).

No activity uses the layer yet. The [document workflows plan](plans/document-workflows.md) describes the first consumers: storage optimization (detection) and photo to document (recognition).

## Results

| Call | Returns |
| --- | --- |
| `Detect(ctx, img, DetectOptions{MinScore})` | `Detection`: the regions in reading order, each a `Quad` with the detector's `Score` (0 to 1). Also the size of the preview the detector analyzed, because regions only a few preview pixels high are unreliable. |
| `Recognize(ctx, img)` | `Page`: its `Lines` in reading order, each with `Text`, `Confidence` (the mean of its characters), `Quad` and `Words`, each word with its own `Quad`. `Page.Text()` joins the lines. An image without text gives a page without lines. |
| `Info()` | the engine's name and its model files with their SHA-256, for run metrics |

Coordinates:

- A `Quad` holds four corners in pixels from the image's top-left corner, in the text's reading order: top left, top right, bottom right, bottom left. A line on an upside-down page therefore starts at the bottom right.
- `Quad.Height()` is the shortest side: the height of the text line, also when the text is rotated.

Scripts without spaces (Chinese, Japanese, Korean) have one word per character.

## PaddleOCR engine

The engine runs the same models, preprocessing and thresholds as PaperDotNet, so both applications read text the same way. The models are in [`models/paddleocr`](../models/paddleocr/README.md):

1. **Detection.** PP-OCRv6 small DBNet runs on a bounded preview:
   - The preview's longest side is at most `MaxDetectionDimension` (default 1536), and smaller for square images, so it stays near one megapixel.
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

ONNX Runtime is loaded at run time through [purego](https://github.com/ebitengine/purego), so PaperGo still builds with `CGO_ENABLED=0`.

`internal/ocr/paddle` calls the ONNX Runtime C API (version 23, ONNX Runtime 1.23 or newer) directly:

- Models load from memory, which works the same on every platform.
- Input tensors live in ONNX Runtime's memory.
- The CPU arena and memory patterns are off, because image shapes vary and retained buffers would grow a long-running process.

What this means for deployments:

- **A dynamically linked binary.** A binary that imports `internal/ocr/paddle` is linked against the platform's C library, so it cannot run in a `scratch` image. Today that is only `cmd/ocr`. The API binary and the Docker image change when the first OCR activity ships.
- **The shared library:** `libonnxruntime.so` (Linux, glibc), `libonnxruntime.dylib` (macOS) or `onnxruntime.dll` (Windows). `scripts/fetch-onnxruntime.sh` downloads the pinned Linux release (1.23.2, x64 or arm64), checks its SHA-256 and prints the library's path.

Configuration (`paddle.Config`):

| Field | Meaning |
| --- | --- |
| `Library` | path of the ONNX Runtime shared library |
| `Models` | directory of the model files |
| `Threads` | CPU threads per inference, 1 to 32 (default 1) |
| `MaxDetectionDimension` | longest side of the detection preview, a multiple of 32 from 256 to 2600 (default 1536) |

`New` checks the configuration and hashes the model files. The library and the models load on first use, and `Close` releases the models.

### Performance

Measured on a 4-core x86-64 container with one thread:

- **A 960×960 detection:** about 330 ms.
- **One 48×320 text line:** about 21 ms. Wider lines take longer.
- **An A4 page of 45 lines (1240×1754):** 5.7 s with one thread, 3.9 s with four.

Inference dominates; image preparation in Go takes about 15% of the time.

## Trying it

`cmd/ocr` runs the engine on JPEG, PNG or WebP files and prints JSON. It does not apply EXIF orientation.

```bash
export ONNXRUNTIME_LIB=$(bash scripts/fetch-onnxruntime.sh)
go run ./cmd/ocr receipt.jpg              # lines, words and text
go run ./cmd/ocr -detect -min-score 0.5 receipt.jpg
```

## Tests

`internal/ocr/paddle` has two kinds of tests:

- **Unit tests** cover the post-processing geometry, CTC decoding, word placement and rectification.
- **Engine tests** render text with the Go fonts and check what the real models read. They cover upside-down and transparent pages, blank pages, concurrent use and cancellation.

The engine tests need `ONNXRUNTIME_LIB`. They are skipped without it, unless `PAPERGO_REQUIRE_OCR` is set. CI installs the pinned release and requires them.
