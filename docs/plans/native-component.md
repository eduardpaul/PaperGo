# CGO-free PaperGo with an optional native component

Status: **implementation plan**. It proposes the Core sockets under the Extension-First Protocol for review; nothing here is implemented yet. Baseline inspected: `main` at `485be49`.

## Outcome

One branch, one repository, two deliverables:

- **PaperGo** (the root module, `papergo`) builds every executable with `CGO_ENABLED=0`: one static binary per command for any `GOOS`/`GOARCH`. On its own it reads PDFs and WebP in pure Go, searches with the SQLite store, and uses **external providers** for AI: OpenAI-compatible embeddings, OpenAI-compatible chat decisions and an HTTP OCR service.
- **PaperGo Native** (a second Go module in `native/`, `papergo/native`) is built with `CGO_ENABLED=1` and links every C/C++/Rust dependency: ONNX Runtime, HF tokenizers, zvec, and the PaddleOCR, EmbeddingGemma/Harrier and Laya engines. It ships as a **separate, optional package**: the `papergo-native` executable with its `lib/` and models. When it is installed, PaperGo starts it as a child process and its providers (`local` embeddings, `laya` decisions, `paddle` OCR, `zvec` search) become available. When it is absent, those providers are not offered and PaperGo runs exactly as above.

For a .NET reader: the root module is the application project; `native/` is a separate project with its own dependency file (like a separate `.csproj` with `runtimes/<rid>/native` assets), shipped as an optional add-on. The two talk out of process, like an app and a gRPC/named-pipe worker service, because Go has no in-process equivalent of loading an optional assembly (see [why out of process](#why-out-of-process)).

## CGO inventory and where each piece goes

| Dependency | Package(s) today | Destination |
| --- | --- | --- |
| `github.com/zvec-ai/zvec-go` | `internal/dal/search/zvecstore` | `native/`; PaperGo's default store is `sqlite` (pure Go, keyword + semantic + hybrid) |
| `github.com/yalue/onnxruntime_go` | `internal/onnx` | `native/` |
| `github.com/daulet/tokenizers` | `internal/embedding`, `internal/decision/laya.go` | `native/` |
| `internal/nativeheap` (`import "C"`) | native engines only | `native/` |
| `internal/embedding`, `internal/ai/local.go` | local embeddings | `native/`, as the `local` provider |
| Laya engine: `internal/decision/{laya,bundle}.go`, `manifest.json`, `LICENSE`, `NOTICE` | local decisions | `native/`, as the `laya` provider; question model, validation, serializers and answers stay in `internal/decision` |
| `internal/ocr/paddle` | OCR | `native/`, as the `paddle` provider |
| `github.com/klippa-app/go-pdfium` `single_threaded` | `internal/doctext/pdfium` | **stays in PaperGo**, switched to go-pdfium's `webassembly` backend (embedded `pdfium.wasm` on wazero, same API, ~1.8–2.2× native render time upstream) |
| `github.com/kolesa-team/go-webp` | `internal/imaging` | **stays in PaperGo**, replaced by cgo-free `github.com/gen2brain/webp` (libwebp on wazero; check licence and libwebp version first) |
| `cmd/embed`, `cmd/decide`, `cmd/ocr` | diagnostics | `native/cmd/...` |
| `scripts/native.py`, `native.json`, `native-notices`, `decision-model`, `*-benchmark`, `fetch-embedding-models.py`, `prepare-preset-models.py`, `model-notices`, `models/paddleocr` | SDK and model tooling | `native/scripts`, `native/models` |

`modernc.org/sqlite`, `pgx`, `goja`, `esbuild` and the MCP SDK are already pure Go.

## Repository layout

```
go.mod                      module papergo           (no cgo dependency)
cmd/api, cmd/admin, cmd/mcp …                        CGO_ENABLED=0
internal/…                                           incl. the provider registries,
internal/nativehost/                                 the native protocol and its client
native/
  go.mod                    module papergo/native    require papergo; replace papergo => ../
  cmd/papergo-native/                                the host executable (cgo)
  cmd/embed, cmd/decide, cmd/ocr                     diagnostics (cgo)
  internal/onnx, embedding, laya, paddle, zvecstore, nativeheap
  scripts/, models/
go.work                     use . ./native           (development only)
```

- **Separate module, so the root `go.mod`/`go.sum` never mention cgo libraries.** `go build ./...` at the root needs no SDK; `go mod tidy` at the root drops zvec (and its `replace`), onnxruntime, tokenizers and go-webp.
- `papergo/native` may import `papergo/internal/...`: Go's `internal` rule is by import-path prefix, and `papergo/native` is under `papergo/`. The host reuses PaperGo's types (`search.Document`, `ocr.Page`, `decision.Request`, …) directly, so the protocol has no hand-written duplicates.
- Root `go.work` lets editors and `go test` see both modules together; CI builds each module on its own.

## Sockets in PaperGo (needs approval)

### 1. Provider registries

Today `cmd/api/main.go` constructs native engines directly and `internal/config` hard-codes `"none, local or openai"` and `"zvec, sqlite or none"`. Replace that with four registries of name → provider, each producing a port that already exists:

| Registry | Port | Registered by PaperGo | Registered when the native component is installed |
| --- | --- | --- | --- |
| `ai.RegisterEmbeddingProvider` | `ai.Embedder` | `none`, `openai` | `local` |
| `decision.RegisterProvider` | `decision.Model` | `none`, `openai` | `laya` |
| `ocr.RegisterProvider` | `ocr.Engine` | `none`, `paddlex` (HTTP) | `paddle` |
| `search.RegisterStore` | `search.Store` | `sqlite` | `zvec` |

- Each provider owns and validates its settings; `internal/config` keeps only the provider names and an opaque validated config. Native-only settings (`ONNXRUNTIME_LIB`, `OCR_MODELS`, `OCR_THREADS`, `AI_EMBEDDINGS_MODELS_PATH|THREADS|MAX_TOKENS`, `AI_DECISION_MODELS_PATH|THREADS|MAX_TOKENS|HEAD_TOKENS`, `SEARCH_ZVEC_PATH`, zvec keyword candidates) are read by the native component, not PaperGo.
- Error messages list the providers actually available ("must be none or openai; install PaperGo Native for local").
- Defaults follow availability: with the component installed, `SEARCH_STORE` defaults to `zvec` and OCR, Laya and local embeddings enable from their bundled assets, as today; without it, `sqlite`, and AI is off until configured.
- Provider names are typed constants (`AGENTS.md`, fixed value sets). `internal/backup` and `cmd/admin search copy` stop spelling `"zvec"`: they ask the store, through the registry, whether its snapshots must be taken offline and how to open it.

### 2. The native host protocol (`internal/nativehost`)

A pure-Go client in PaperGo and a server in `native/` share one package that defines the wire contract.

- **Discovery.** `PAPERGO_NATIVE_PATH`, defaulting to `native/papergo-native[.exe]` beside the PaperGo executable (`/opt/papergo/native` in the image). Absent → no native providers are registered.
- **Launch and handshake.** PaperGo starts the host as a child process with a private socket path and a random token. It listens on a Unix domain socket (Windows 10+ supports AF_UNIX, and Go does too) in a `0700` directory. The first call exchanges protocol version and build version; a mismatched component is refused with a message naming both versions. Host logs go to PaperGo's log with a `native` attribute.
- **Transport.** HTTP/1.1 over that socket, standard library on both sides: request contexts carry cancellation (ONNX inference is already cancellable), streamed bodies carry `search.Store.Export`, and `encoding/gob` bodies keep `[]float32` vectors and pixel buffers compact. One endpoint per port method (`/v1/embedder/embed`, `/v1/search/upsert-documents`, …).
- **Client adapters.** Each native provider in PaperGo is a thin client implementing the port: `ai.Embedder` (4 methods), `decision.Model` (2), `ocr.Engine` (4), `search.Store` (24, including streamed export/import). No engine logic lives on the client side.
- **Lifecycle.** One host process serves every native engine, so ONNX Runtime stays shared by OCR, embeddings and Laya as today. The host keeps its own idle unloading (five minutes) and `malloc_trim`. PaperGo starts it on first use, restarts it after a crash with backoff, and stops it on shutdown. A call that dies with the host returns a retryable error; operations are already repeatable at this boundary ([durability boundaries](../architecture.md#durability-boundaries)), so a crash costs a retry, not data. zvec's one-process-per-directory rule holds because only the host opens the directory.
- **Images for OCR.** `ocr.Engine` takes an `image.Image`. The client sends raw RGBA in a streamed body (a 64-MP maximum is 256 MiB of local copy). If that measures too slow, a shared temp file mapped by both processes is the next step, behind the same interface.

### Why out of process

| Approach | Verdict |
| --- | --- |
| **Child process + local socket** (this plan) | PaperGo stays static and cgo-free. The native code keeps its existing cgo bindings almost unchanged. A native crash cannot take the server down. Costs a local round trip per call (sub-millisecond; negligible next to inference, small next to a search). |
| Go `plugin` package (`.so` loaded at runtime, the nearest thing to loading an assembly) | Rejected: needs cgo in the host, no Windows support, exact same toolchain and dependency versions in both builds, no unloading. |
| `ebitengine/purego` (dlopen without cgo, the nearest thing to P/Invoke) | Possible but not chosen: every binding (ONNX Runtime's function table, zvec's C API, a cdylib build of tokenizers) must be rewritten by hand, ABI mistakes surface at run time, native crashes still kill the server, and the Linux binary becomes dynamically linked to glibc again. |
| Build tags producing two builds of the same binary | Rejected: that gives two editions of PaperGo, not one PaperGo plus an optional component. |

## External providers in PaperGo

These give PaperGo AI features without the native component.

### Embeddings

Already supported: `AI_EMBEDDINGS_PROVIDER=openai` with any OpenAI-compatible `/v1/embeddings` endpoint (OpenAI, Ollama, LM Studio, vLLM, llama.cpp). Removing the in-process `local` provider from `internal/ai` is the only change. The engine key already includes provider and model, so vectors never mix across providers; switching provider re-embeds.

### Decisions through a chat model

`decision.Model` returns per-option probabilities. A chat model can give real probabilities through token log-probabilities:

1. Render each row with the existing serializers (text, canonical JSON, document text with context), the question's instructions, and its options labelled `A`…`T` (≤ 20, the existing limit). Ask for the letter only.
2. Call `/v1/chat/completions` with `temperature: 0`, `max_tokens: 1`, `logprobs: true`, `top_logprobs: 20`.
3. Take the softmax of the labels' log-probabilities, renormalized over valid labels; a label outside the top 20 scores 0, and no valid label is a row error.
4. Keep the existing result semantics: `choice` picks the highest (ties → first authored), `noul` returns P(true), `score` the expected level index.

Details:

- `decision.Result` keeps `Index`, `Probabilities`, `Score` and `Noul`. Laya's `ActLogits`, `Logits` and `Tokens` move to the `laya` provider's diagnostics (`native/cmd/decide`). The OpenAPI contract does not change: it names no engine and exposes no logits.
- Limits come from the provider. Character limits stay in `internal/decision`; Laya's token budgets (context, head, 48 tokens per option, eight-row batches) move with Laya.
- Settings: `AI_DECISION_PROVIDER`, `AI_DECISION_MODEL`, `AI_DECISION_ENDPOINT`, `AI_DECISION_API_KEY_FILE`; empty values take the chat provider's, as `AI_BATCH_*` does. Startup refuses a model that returns no log-probabilities (reasoning models usually don't).
- `ID()` is `openai:<model>:<endpoint hash>:labels-logprobs-v1`, so cached results never mix with Laya's.
- A busy or failing provider defers the operation by `Retry-After`, like immediate completions, without holding a `decisions` worker.
- Cost: `taxonomy.decide` in `multiple` mode makes one call per candidate (up to 50), run with bounded concurrency and covered by the result cache.
- Docs say plainly that external decisions send document text to the provider.

### OCR through an HTTP service

`ocr.Engine` needs positions: quads for regions, lines and words, for image optimization and searchable PDFs. A vision chat model cannot supply them. The `paddlex` provider calls a PaddleX/PaddleOCR serving endpoint (`/ocr`: detection polygons, recognition polygons, texts, scores, optional word boxes) with `OCR_ENDPOINT` and `OCR_API_KEY_FILE`, run as a sidecar in `deploy/compose.yaml`. Without any OCR provider, OCR is off and activities that need it fail with the existing message; PDF text layers still work.

## Packaging and release

| Artifact | Contents | Built |
| --- | --- | --- |
| `papergo-<version>-<os>-<arch>.zip` | `papergo`, `papergo-admin`, `papergo-mcp`, notices | cross-compiled from one Linux runner, `CGO_ENABLED=0` |
| `papergo-native-<version>-<os>-<arch>.zip` | `native/papergo-native`, `native/lib/` (ONNX Runtime, zvec, compiler runtimes), `native/models/` (PaddleOCR, EmbeddingGemma 2, Harrier, Laya), native and model notices | per-OS native jobs, as release CI does today |
| image `papergo` | static binaries on `distroless/static` | root module only |
| image `papergo-native` | `FROM` the same binaries on `distroless/cc`, plus `/opt/papergo/native` | Dockerfile target `native`, reusing today's toolchain, Laya export and model-asset stages |

Installing the component means extracting its ZIP beside PaperGo's executables (or using the `papergo-native` image). Versions must match exactly; the handshake enforces that. The Dockerfile keeps a single toolchain definition; its native stages build only the `native` target.

## Developer workflow and checks

- `go build ./...`, `go test ./...` and `go vet ./...` at the root with `CGO_ENABLED=0`, on any OS, with no SDK installed.
- `scripts/check.py` adds a guard that fails if any root package contains `import "C"` or the root `go.mod` requires a cgo module.
- `native/` keeps today's setup: `python3 native/scripts/native.py`, `source .tools/native/env.sh`, the `PAPERGO_REQUIRE_EMBEDDING` / `PAPERGO_REQUIRE_OCR` gates, Docker smoke.
- An end-to-end test starts a built `papergo-native` through the real client and runs each port's contract tests (`internal/search/storetest` already exists for stores; add equivalents for embedders, decision models and OCR engines). The same contract tests run against in-process fakes in the root module, so the protocol is tested without native libraries.
- `scripts/dev.sh` starts PaperGo with the component when `native/` has been built, without it otherwise.

## Order of work

| Step | Change | Verifiable by |
| --- | --- | --- |
| 1 | Provider registries; native engines still registered in-process; behaviour unchanged | existing suite green; per-provider config tests |
| 2 | `internal/nativehost` protocol, client adapters, port contract tests against in-process fakes | contract tests in the root module |
| 3 | Create `native/` module; move the engines, diagnostics, scripts and models there with `git mv`; add `cmd/papergo-native`; remove in-process native registration from PaperGo | `CGO_ENABLED=0 go build ./...` at the root; native gates and end-to-end contract tests in `native/` |
| 4 | PDFium WASM backend (rename the reader's `Name()` so cached text is not reused across backends; pass an empty `wazero.FSConfig` so PDFium sees no host files); cgo-free WebP | doctext, preview, optimize, compose tests; measured latency in `docs/performance-artifacts` |
| 5 | Chat decider and `paddlex` OCR provider | `httptest` provider tests, decision cache-key tests, workflow samples against fakes |
| 6 | Dockerfile targets, release jobs, compose, `import "C"` guard | `scripts/check.py`, release dry run, both images' smoke tests |
| 7 | Docs: `ai.md`, `decisions.md`, `ocr.md`, `search.md`, `embeddings.md`, `dependencies.md`, `architecture.md`, `docker.md`, `releases.md`, `development.md`, `performance.md`, `deploy/`, README, each describing PaperGo with and without the native component | docs link check |

Each step lands on `main` on its own.

## Open decisions

1. **Transport**: standard-library HTTP + gob over a Unix socket (recommended: no code generation, cancellation and streaming built in), or `hashicorp/go-plugin` with gRPC (process supervision included, but adds protobuf definitions and generation to the build).
2. **OCR without the component**: the `paddlex` HTTP provider (recommended), or no OCR unless the component is installed.
3. **PDFium in the component**: none (recommended; WASM in PaperGo covers it), or a native `pdf` provider in the host for ~2× faster rendering.
4. **Decisions without log-probabilities**: refuse such models (recommended), or accept structured-output answers with probability 1.
5. **WebP library**: `gen2brain/webp` (recommended, full libwebp) or pure-Go `HugoSmits86/nativewebp` (lossless only, so larger previews).
