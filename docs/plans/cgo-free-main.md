# CGO-free main and the native branch

Status: **implementation plan**. It proposes the Core sockets under the Extension-First Protocol for review; nothing here is implemented yet. Baseline inspected: `main` at `485be49`.

## Outcome

- **`main`** builds every executable with `CGO_ENABLED=0`. `go build ./...` gives one static binary per command for any `GOOS`/`GOARCH`, with no native SDK, no `lib/` directory, no compiler runtimes and no model assets. AI features use **external providers**: OpenAI-compatible embeddings (already supported), OpenAI-compatible chat decisions (new), and an external OCR service (new). Search uses the pure-Go SQLite store.
- **`native`**, a long-lived branch created from `main`, adds the in-process engines as **extensions that register into `main`'s sockets**: local embeddings (EmbeddingGemma 2 / Harrier on ONNX Runtime + HF tokenizers), the zvec search store, Laya decisions and PaddleOCR. It keeps today's native SDK installer, model assets and release layout.

The native branch must be an *additive* diff over `main`: new packages, one registration file per executable, and build/release/doc changes. If a native change needs to edit a Core file, the socket in `main` is missing something and gets fixed in `main` first. That is what keeps merging `main` into `native` cheap.

## CGO inventory

| Dependency | Package(s) | Used for | Decision on `main` |
| --- | --- | --- | --- |
| `github.com/zvec-ai/zvec-go` | `internal/dal/search/zvecstore` | default search store | **Moves to `native`.** `main` defaults to `SEARCH_STORE=sqlite` (pure Go, `modernc.org/sqlite`, already implements keyword, semantic and hybrid search). |
| `github.com/yalue/onnxruntime_go` | `internal/onnx` | runtime of OCR, embeddings, Laya | **Moves to `native`.** |
| `github.com/daulet/tokenizers` | `internal/embedding`, `internal/decision/laya.go` | HF Rust tokenizer | **Moves to `native`.** |
| `internal/nativeheap` (`import "C"`) | embedding, Laya, zvec, Paddle | glibc `malloc_trim` | **Moves to `native`**; only native engines use it. |
| `internal/embedding`, `internal/ai/local.go` | `AI_EMBEDDINGS_PROVIDER=local` | local embeddings | **Moves to `native`.** `main` keeps `openai`. |
| `internal/decision` (Laya engine, bundle, manifest) | `ai.decide`, `taxonomy.decide` | local decisions | **Split.** Question model, validation, canonical serialization and answers stay in `main`; the Laya engine moves to `native`. `main` gets a chat-model decider. |
| `internal/ocr/paddle` | OCR, image optimization, searchable PDFs | PaddleOCR | **Moves to `native`.** `main` gets an external OCR engine (see [OCR](#ocr-on-main)). |
| `github.com/klippa-app/go-pdfium` `single_threaded` | `internal/doctext/pdfium` | PDF text layers and page rendering | **Stays on `main`, switched to go-pdfium's `webassembly` backend** (embedded `pdfium.wasm` on wazero, no cgo, same `Pool` API). Upstream measures it at ~1.8–2.2× native render time. `native` may keep the cgo backend (see [open decisions](#open-decisions)). |
| `github.com/kolesa-team/go-webp` | `internal/imaging` | WebP decode/encode (previews, optimization, OCR input) | **Stays on `main`, replaced by a cgo-free WebP library**: `github.com/gen2brain/webp` (libwebp compiled to WASM on wazero; encode + decode + animation detection). Check licence and its libwebp version before adopting. |

Nothing else links C: `modernc.org/sqlite`, `pgx`, `goja`, `esbuild` and the MCP SDK are pure Go. `cmd/mcp` already builds with `CGO_ENABLED=0` in `scripts/check.py`.

## Sockets to add in Core (needs approval)

Today the native engines are constructed directly in `cmd/api/main.go` and validated by hard-coded switches in `internal/config` (`"none, local or openai"`, `"zvec, sqlite or none"`). Replace that with four small provider registries. Each is a name → factory map populated by `init()` in provider packages, read by config validation and by composition. `main` registers its pure-Go providers; `native` adds its own by importing a package.

| Socket | Port it produces | `main` registers | `native` registers |
| --- | --- | --- | --- |
| `ai.RegisterEmbeddingProvider(name, factory)` | `ai.Embedder` (exists) | `none`, `openai` | `local` |
| `decision.RegisterProvider(name, factory)` | `decision.Model` (exists: `ID`, `Decide`) | `none`, `openai` | `laya` |
| `ocr.RegisterProvider(name, factory)` | `ocr.Engine` (exists) | `none`, the external engine | `paddle` |
| `search.RegisterStore(name, opener)` | `search.Store` (exists) | `sqlite` | `zvec` |

Shape, common to all four:

```go
type Provider[C, P any] struct {
    // Configure reads its own settings through the lookup and validates them
    // without loading anything (no files opened, no libraries loaded).
    Configure func(lookup func(string) string, paths Paths) (C, error)
    // Open builds the port from the validated settings; native engines stay lazy.
    Open func(ctx context.Context, cfg C) (P, error)
}
```

Consequences:

- **Settings belong to their provider.** `ONNXRUNTIME_LIB`, `OCR_MODELS`, `OCR_THREADS`, `AI_EMBEDDINGS_MODELS_PATH`, `AI_EMBEDDINGS_THREADS`, `AI_EMBEDDINGS_MAX_TOKENS`, `AI_DECISION_MODELS_PATH`, `AI_DECISION_THREADS`, `AI_DECISION_MAX_TOKENS`, `AI_DECISION_HEAD_TOKENS`, `SEARCH_ZVEC_PATH` and the zvec keyword-candidate setting leave `internal/config` and live in the native providers. `config.Config` keeps only provider names plus an opaque, validated provider config.
- **Defaults are registry-driven.** `SEARCH_STORE` defaults to the highest-priority registered store: `sqlite` on `main`, `zvec` on `native`. The asset auto-detection that enables OCR, Laya and local embeddings when `assets/models/...` sits beside the executable moves into the native providers' `Configure`.
- **Error messages list registered names**, so `main` says "must be none or openai" and `native` "must be none, local or openai" without either branch hard-coding the other.
- **Fixed value sets** (`AGENTS.md`): provider names are typed constants declared next to each provider, not string literals in `config`, `backup` or `cmd/admin`. `internal/backup` stops spelling `"zvec"`: a store reports through the registry whether its snapshots must be taken offline.
- **`cmd/admin search copy`** opens both stores through the registry, so it builds on `main` (sqlite only) and gains `zvec` on `native` with no edit.
- **YAML** (`internal/config/yaml.go`): the `search.zvec_path` key and other native keys become a provider-owned `providers:` map instead of fixed fields, so the YAML schema on `main` carries no native-only keys.

Other uses of the same sockets: hosted embedding/OCR/decision services, a future GPU or llama.cpp engine, test fakes without build tags (`internal/ai/aitest`, `internal/testutil/searchstore`), and an external search store.

## Decisions on `main`: chat-model decider

`decision.Model` takes rows of `{State, Question, Options, Type}` and returns per-option probabilities. A chat model can produce real probabilities, not just a pick, through token log-probabilities:

1. Render each row as a fixed prompt: the state (text, canonical JSON, or document text with context, using the existing serializers), the question's instructions, and its options labelled with single-token letters `A`…`T` (≤ 20 options, matching the existing limit). Ask for the letter only.
2. Call `/v1/chat/completions` with `temperature: 0`, `max_tokens: 1`, `logprobs: true`, `top_logprobs: 20`.
3. Probabilities are the softmax of the labels' log-probabilities, renormalized over the valid labels; a label absent from the top 20 gets probability 0. No valid label in the answer is a row error.
4. Map to the existing result types unchanged: `choice` picks the highest (ties → first authored), `noul` returns P(true), `score` returns the expected level index.

Contract and plumbing:

- `decision.Result` loses the Laya-only fields (`ActLogits`, `Logits`, `Tokens`) on `main`; they move to Laya's own diagnostic type in `native` (`cmd/decide`). Workflow outputs already exclude them, so the OpenAPI response shape is unchanged.
- Limits on `main` are character-based (existing 16 KiB inline/24 KiB definitions bounds); token budgets (`AI_DECISION_MAX_TOKENS`, head tokens, 48 tokens per option, eight-row batches) are Laya's and move with it. `decision.Requests` takes a limiter from the provider instead of assuming Laya's.
- Settings: `AI_DECISION_PROVIDER=none|openai`, `AI_DECISION_MODEL`, `AI_DECISION_ENDPOINT`, `AI_DECISION_API_KEY_FILE`, empty values taking the chat provider's, as `AI_BATCH_*` does. Startup refuses a model that returns no log-probabilities (one probe call, cached per model), since reasoning models typically do not.
- `ID()` is `openai:<model>:<endpoint hash>:labels-logprobs-v1`, so cache keys never mix with Laya's.
- Busy or failing providers defer the operation by `Retry-After` exactly like immediate completions (`internal/ai/http.go`), without holding a `decisions` worker.
- Cost: `taxonomy.decide` in `multiple` mode is one call per candidate (up to 50). Rows of one operation run with bounded concurrency (`workers.decisions` config), and the existing result cache makes reruns free. Routing decisions through `ai.batch` is a later option, not part of this plan.
- Docs state plainly that external decisions send document text to the provider.

## Embeddings on `main`

Already supported: `AI_EMBEDDINGS_PROVIDER=openai` with any OpenAI-compatible `/v1/embeddings` (OpenAI, Ollama, LM Studio, vLLM, llama.cpp). Changes:

- Delete `ProviderLocal`, `local.go`, the `embedding` import in `internal/ai/embeddings.go` and the local branch of `EmbeddingsConfig.Validate`; the native branch re-adds them as a registered provider.
- `docs/embeddings.md` becomes the native branch's page; `main` documents self-hosting EmbeddingGemma through Ollama/llama.cpp. Vectors from different engines never mix (the engine key already includes provider and model), so switching provider means re-embedding.

## OCR on main

`ocr.Engine` needs both `Detect` (regions, for image optimization) and `Recognize` (lines and words with quads, for searchable PDFs). A vision chat model returns text but no reliable positions, so it cannot implement the port. Recommended: an **HTTP OCR engine** speaking the PaddleX/PaddleOCR serving API (`/ocr`, which returns `dt_polys`, `rec_polys`, `rec_texts`, `rec_scores` and, with word boxes on, word polygons), configured with `OCR_PROVIDER=paddlex`, `OCR_ENDPOINT`, `OCR_API_KEY_FILE`. It keeps the same models as the native engine, run in a sidecar container in `deploy/compose.yaml`. Without a provider, OCR is off and activities that need it fail with the existing message; text layers still work through PDFium WASM.

## Repository changes on `main`

1. **Sockets.** Add the four registries, move config parsing of provider-specific settings into providers, and register the pure-Go providers. Keep behaviour identical in this step (native providers still registered) so it can be reviewed and tested alone.
2. **PDF and WebP.** Switch `internal/doctext/pdfium` to the `webassembly` pool (rename the reader `Name()` so cached text is not reused across backends; size the pool to the `documents` worker). Replace `go-webp` in `internal/imaging` with the cgo-free library; check optimization and preview golden tests and record measured latency in `docs/performance-artifacts`.
3. **New external providers.** The chat decider and the HTTP OCR engine, each with `httptest` fakes in tests.
4. **Cut the native code.** Delete `internal/onnx`, `internal/embedding`, `internal/ai/local.go`, `internal/nativeheap`, `internal/ocr/paddle`, the Laya engine files of `internal/decision` (`laya.go`, `bundle.go`, `manifest.json`, `LICENSE`, `NOTICE`, model tests and testdata), `internal/dal/search/zvecstore`, `internal/backup/zvec_test.go`, `cmd/embed`, `cmd/decide`, `cmd/ocr`, `models/paddleocr`, `scripts/native.py`, `scripts/native.json`, `scripts/native-notices`, `scripts/decision-model`, `scripts/decision-benchmark`, `scripts/embedding-benchmark`, `scripts/fetch-embedding-models.py`, `scripts/prepare-preset-models.py`, `scripts/model-notices`. `go mod tidy` drops zvec (and its `replace`), onnxruntime, tokenizers and go-webp.
5. **Build.** `scripts/build-binaries.py` loses `LIBRARIES` and compiler runtimes and builds with `CGO_ENABLED=0`; `scripts/check.py` builds and vets `./...` with `CGO_ENABLED=0` and fails if any package contains `import "C"`. The Dockerfile drops `laya-export`, `laya-model`, `model-assets`, CMake and the native SDK stage; the runtime image moves from `distroless/cc` to `distroless/static`. Release CI cross-compiles every target from one Linux runner instead of per-OS native jobs, and the assets ZIP no longer exists unless something else needs it.
6. **Docs.** Update `ai.md`, `decisions.md`, `ocr.md`, `search.md`, `embeddings.md`, `dependencies.md`, `architecture.md`, `docker.md`, `releases.md`, `development.md`, `performance.md`, `deploy/README.md`, `deploy/config.yaml` and the README to describe only the pure-Go app with external providers, plus one short section pointing to the `native` branch for in-process engines. The OpenAPI contract does not change: it names no engine and exposes no logits.

## The native branch

Create `native` from `main` immediately after step 1 lands, *before* step 4 deletes anything, so the native code moves into its new shape with history instead of being re-imported:

1. Wrap the existing engines as providers: `internal/embedding` + a `local` embedding provider, `internal/decision/laya` (the Laya engine as a `laya` decision provider), `internal/ocr/paddle` as `paddle`, `internal/dal/search/zvecstore` as `zvec` with highest default priority. Keep `internal/onnx` and `internal/nativeheap`.
2. Add one registration file per executable, e.g. `cmd/api/native.go` with `import _ "papergo/internal/native"`, where `internal/native` imports every native provider. `cmd/admin` imports the zvec store the same way for `search copy`.
3. Keep `cmd/embed`, `cmd/decide`, `cmd/ocr`, the native installer, model scripts, `CGO_ENABLED=1` builds, the native Dockerfile stages and per-OS release jobs, all on this branch only.
4. Docs on the branch add back the native pages (`embeddings.md`, Laya in `decisions.md`, PaddleOCR in `ocr.md`, zvec in `search.md`, the native section of `dependencies.md`).

Keeping it current:

- Flow is one-way: work lands on `main`, and `main` is merged (not rebased) into `native` regularly, at least before each release. Native-only fixes land on `native`; anything touching a socket lands on `main` first.
- CI runs on both branches; `native` additionally runs the native test gates (`PAPERGO_REQUIRE_EMBEDDING`, `PAPERGO_REQUIRE_OCR`) and the Docker smoke test.
- Releases: `main` publishes `papergo` artifacts and image; `native` publishes `papergo-native` artifacts and image from the same version tag.

## Order of work

| Step | Branch | Depends on | Verifiable by |
| --- | --- | --- | --- |
| 1. Sockets, behaviour unchanged | `main` | — | existing test suite green, config tests per provider |
| 2. Create `native`, wrap engines as providers | `native` | 1 | native gates and Docker smoke on `native` |
| 3. PDFium WASM, cgo-free WebP | `main` | 1 | doctext, preview, optimize, compose tests; perf artifacts |
| 4. Chat decider, HTTP OCR engine | `main` | 1 | `httptest` provider tests, decision cache-key tests, workflow samples against a fake |
| 5. Cut native code, `CGO_ENABLED=0` everywhere | `main` | 2, 3, 4 | `CGO_ENABLED=0 go build ./...`, the `import "C"` guard, cross-compiled release dry run |
| 6. Docs, Dockerfile, release, compose | `main` and `native` | 5 | `scripts/check.py`, docs links, image smoke |
| 7. Merge `main` into `native` | `native` | 6 | native CI green |

## Open decisions

1. **OCR on `main`**: the PaddleX-compatible HTTP engine (recommended), or OCR off on `main` entirely.
2. **PDFium on `native`**: keep the WASM backend there too (smaller diff, one code path) or keep the cgo backend for its ~2× render speed. Recommended: WASM on both until measured as a bottleneck.
3. **WebP library**: `gen2brain/webp` (full libwebp feature set via WASM, recommended) or a pure-Go encoder such as `HugoSmits86/nativewebp` (lossless only, which would grow previews).
4. **Decisions without log-probabilities**: refuse such models (recommended, keeps probabilities meaningful) or accept structured-output answers with probability 1.
5. **Branch versus build tags**: a separate branch is what this plan delivers. The same sockets would also allow one branch with a `//go:build native` registration file, which avoids merge upkeep; worth reconsidering if `native` drifts.
