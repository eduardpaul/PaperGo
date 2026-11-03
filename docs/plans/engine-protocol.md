# Inference engine protocol (version 1)

An inference engine is a separate process that runs PaperGo's ONNX features:
PaddleOCR text recognition, local embeddings and Laya decisions. The core
starts it, talks to it over HTTP and keeps everything else (decoding images,
storage, shortening passages, caching) to itself. This page is the contract
an engine and the core's client both implement. The experimental .NET engine
in `engines/dotnet/` implements it.

## Transport

- HTTP/1.1, JSON bodies in UTF-8 except the OCR request body.
- `--listen unix:/path/to.sock`: a Unix domain socket created with mode 0600.
  File permissions are the only access control.
- `--listen tcp:127.0.0.1:PORT`: every request needs
  `Authorization: Bearer <token>`, the content of `--token-file` without
  surrounding whitespace. The engine compares it in constant time and answers
  `401 unauthorized` otherwise. Bind to loopback only.
- `--exit-on-stdin-eof`: the engine exits when its standard input reaches end
  of file. A parent keeps the write end of a pipe open, so the engine dies with
  it on every OS.
- The engine answers `GET /v1/health` only once it listens; models load later.

## Capabilities and configuration

A capability exists when its models are configured and pass the load check at
startup (files present, OCR model digests computed, the Laya manifest equal to
the supported one). Models then load on first use and unload after 5 idle
minutes. Engines take these flags; environment variables are fallbacks with
the core's names:

| Flag | Environment | Meaning |
|---|---|---|
| `--ocr-models DIR` | `OCR_MODELS` | PaddleOCR model directory |
| `--ocr-threads N` | `OCR_THREADS` | threads per inference, 1–32, default 1 |
| `--embeddings-models MODEL=DIR[,MODEL=DIR]` | `AI_EMBEDDINGS_MODEL` + `AI_EMBEDDINGS_MODELS_PATH` | one directory per embedding model |
| `--embeddings-threads N` | `AI_EMBEDDINGS_THREADS` | default 1 |
| `--embeddings-max-tokens N` | `AI_EMBEDDINGS_MAX_TOKENS` | default 512 |
| `--decision-models DIR` | `AI_DECISION_MODELS_PATH` | verified Laya bundle |
| `--decision-threads N` | `AI_DECISION_THREADS` | default 1 |
| `--decision-max-tokens N` | `AI_DECISION_MAX_TOKENS` | default 1024 |
| `--decision-head-tokens N` | `AI_DECISION_HEAD_TOKENS` | default 256 |
| `--queue N` | | requests that may wait per model, default 16 |

## Endpoints

### `GET /v1/health`

`200 {"status":"ok"}`.

### `GET /v1/info`

```json
{"protocol": 1, "version": "0.1.0",
 "ocr": {"engine": "paddleocr", "models": [{"name": "PP-OCRv6_small_det.onnx", "sha256": "…"}]},
 "embeddings": [{"id": "embeddinggemma-2:<rev>:q4:d768:t512", "model": "embeddinggemma-2",
                 "dimensions": 768, "max_tokens": 512}],
 "decisions": {"id": "laya:<rev>:<sha>:fp32:t1024:h256:markers-v1:temperature1:batch8-2048-v1:threads1"}}
```

`ocr` and `decisions` are `null` and `embeddings` is `[]` when not configured.
`ocr` equals the Go engine's `ocr.Info`; the IDs equal `embedding.Engine.ID()`
and `decision.Engine.ID()` for the same configuration, so cached results keep
their keys whichever engine produced them.

### `POST /v1/ocr/detect` and `POST /v1/ocr/recognize`

The body is the decoded, upright image as raw pixels,
`Content-Type: application/octet-stream`, rows top to bottom without padding:

| Header | Value |
|---|---|
| `X-Image-Width`, `X-Image-Height` | positive integers; width × height ≤ 64,000,000 (`ocr.MaxPixels`) |
| `X-Pixel-Format` | `rgba8` (non-premultiplied alpha, 4 bytes), `rgb8` (3 bytes) or `gray8` (1 byte) |

The body must be exactly width × height × bytes per pixel long. Transparent
pixels are composited onto white, as in Go. Detect takes the query parameters
`min_score` (0–1, default 0) and `max_dimension` (0 for the engine default,
otherwise 256–2600). Responses are the Go types' JSON:

- detect: `ocr.Detection`, `{"regions":[{"quad":[{"X":0,"Y":0},…×4],"score":0.9}],"width":…,"height":…}`;
- recognize: `ocr.Page`, `{"width":…,"height":…,"lines":[{"text":…,"confidence":…,"quad":[…],"words":[{"text":…,"quad":[…]}]}]}`.

A quad is four `{"X","Y"}` points because `image.Point` has no JSON tags.
A line without words has `"words": null`, as Go marshals a nil slice.

### `POST /v1/embeddings`

```json
{"model": "embeddinggemma-2", "kind": "query", "texts": ["…"], "dimensions": 0}
```

- `kind` is `query` (the model's retrieval instruction is prepended) or
  `document` (an untitled passage: `title: none | text: …` for EmbeddingGemma,
  the bare text for Harrier).
- `dimensions` is 0 for the model's size, or a smaller Matryoshka size the
  model supports (EmbeddingGemma 128, 256 or 512); the vector space ID then has
  `d<dimensions>`.
- 1–2000 texts, each nonempty UTF-8 without NUL and at most 128 KiB; any
  invalid text rejects the request with `400 invalid_input`.

`200 {"vectors": [[…] | null], "errors": [{"index": 3, "code": "too_many_tokens", "message": "…"}]}`.
Vectors are L2-normalized float32 values in input order. A text over the
token limit has a `null` vector and an error; the other texts are still
embedded. Shortening and retrying is the client's job.

### `POST /v1/decisions`

```json
{"rows": [{"state": "…", "question": "…", "options": ["…", "…"], "type": "choice"}]}
```

`rows` are 1–50 `decision.Request` values. Every row is validated and encoded
before inference; a row that fails gets a `null` result and an error with code
`invalid_input` (validation) or `too_many_tokens` (a lossless budget is
exceeded). The remaining rows run in Go's batches of up to 8 rows and 2048
padded tokens.

`200 {"results": [Result | null], "errors": [{"index", "code", "message"}]}`,
with `Result` as `decision.Result` marshals it:
`{"index","act_logits","logits","probabilities","score"?,"noul"?,"tokens"}`.

## Errors

Failures are `{"code": "…", "message": "…"}`:

| Status | Code | When |
|---|---|---|
| 400 | `invalid_input` | malformed JSON, headers, options or texts |
| 401 | `unauthorized` | missing or wrong bearer token on TCP |
| 404 | `unavailable` | the capability or model is not configured |
| 413 | `too_large` | an image above `ocr.MaxPixels`, a body above its limit, or a text line beyond the OCR memory bound |
| 503 | `busy` | the model's queue is full; `Retry-After` gives seconds |
| 500 | `internal` | inference or model loading failed |

## Concurrency and cancellation

Each model runs one inference at a time (all OCR shares one gate, as in Go).
Requests wait in a bounded queue per model. A client that disconnects leaves
the queue, or terminates its running inference through ONNX Runtime's
`RunOptions.Terminate`; the engine then sends nothing.
