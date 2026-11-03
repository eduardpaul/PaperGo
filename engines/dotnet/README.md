# PaperGo .NET inference engine (experimental)

An experimental engine process that serves PaperGo's ONNX features (PaddleOCR,
EmbeddingGemma 2 and Harrier embeddings, Laya decisions) over the
[engine protocol](../../docs/plans/engine-protocol.md). It ports the Go engines
in `internal/ocr/paddle`, `internal/embedding` and `internal/decision` and is
compared with them by the harness in `bench/` ([results](RESULTS.md)). It is
not wired into the app.

- .NET 10, ASP.NET Core minimal APIs on Kestrel (`CreateSlimBuilder`),
  System.Text.Json source generation, published with NativeAOT.
- ONNX Runtime 1.23.2 through `Microsoft.ML.OnnxRuntime`, the version the Go
  engines load, with the same session options (`internal/onnx`).
- Tokenizers: the Hugging Face Rust tokenizers C ABI from `daulet/tokenizers`,
  the static library the Go build links, linked into the executable through
  `DirectPInvoke` and `NativeLibrary`. Token IDs are the Go engines' IDs.
- Images: the core sends decoded, upright pixels. Resampling ports
  `golang.org/x/image/draw`'s kernel scaler with paddle's Mitchell filter and
  `image/draw`'s compositing, so detection inputs are bit-identical to Go's.

## Layout

| Path | Contents |
|---|---|
| `src/PaperGo.Engine/Protocol` | HTTP endpoints, options, JSON contracts, diagnostics subcommands |
| `src/PaperGo.Engine/Runtime` | ONNX Runtime sessions, the tokenizer binding, per-model gates (queue, idle unload) |
| `src/PaperGo.Engine/Ocr` | PaddleOCR: resampling, DB post-processing, line rectification, CTC decoding |
| `src/PaperGo.Engine/Embeddings` | EmbeddingGemma 2 and Harrier |
| `src/PaperGo.Engine/Decisions` | Laya: bundle verification, markers-v1 encoding, batches |
| `tests/PaperGo.Engine.Tests` | xUnit tests: ported Go unit tests, goldens shared with Go, protocol handlers |
| `bench/` | parity and benchmark harness (`harness.py`), Go reference helper (`goref`) |

## Build, test and publish

The build needs the .NET 10 SDK, gcc, and `libtokenizers.a` from the
repository's native SDK (`scripts/native.py` installs `.tools/native`). Point
`PAPERGO_NATIVE_DIR` (or `-p:PaperGoNativeDir=`) at it when it is elsewhere,
for example from a worktree.

```sh
cd engines/dotnet
dotnet build
dotnet test        # model tests skip when the models are not installed
dotnet publish src/PaperGo.Engine -c Release -r linux-x64 -p:CppCompilerAndLinker=gcc -o out
```

A single-file variant links a static ONNX Runtime 1.23.2 (the CPU build the
Rust `ort` crate downloads) instead of loading `libonnxruntime.so`:

```sh
python3 -I native/fetch-static-onnxruntime.py ../../.cache/ort-static
dotnet publish src/PaperGo.Engine -c Release -r linux-x64 -p:CppCompilerAndLinker=gcc \
  -p:OnnxRuntimeStatic=$PWD/../../.cache/ort-static/libonnxruntime.a -o out-static
```

`-p:CppCompilerAndLinker=gcc` is needed only where clang is not installed.
Trim and AOT warnings (IL2xxx, IL3xxx) fail the build and the publish. The
published directory holds `papergo-engine` and `libonnxruntime.so`, which it
loads from beside itself; `papergo-engine.dbg` holds the debug symbols. For
`dotnet build` and `dotnet test`, a build step links `libtokenizers.a` into
`obj/native/libtokenizers.so`.

## Run

```sh
out/papergo-engine --listen unix:/run/user/1000/papergo-engine.sock --exit-on-stdin-eof \
  --ocr-models models/paddleocr \
  --embeddings-models embeddinggemma-2=bin/assets/models/embedding/embeddinggemma-2,harrier-oss-v1-0.6b=bin/assets/models/embedding/harrier-oss-v1-0.6b \
  --decision-models bin/assets/models/laya

curl --unix-socket /run/user/1000/papergo-engine.sock http://engine/v1/info
```

On TCP, `--listen tcp:127.0.0.1:PORT --token-file FILE` requires
`Authorization: Bearer <token>`. `papergo-engine --help` lists every flag;
each has the core's environment variable as a fallback (`OCR_MODELS`,
`OCR_THREADS`, `AI_EMBEDDINGS_MODEL` + `AI_EMBEDDINGS_MODELS_PATH`,
`AI_EMBEDDINGS_THREADS`, `AI_EMBEDDINGS_MAX_TOKENS`,
`AI_DECISION_MODELS_PATH`, `AI_DECISION_THREADS`, `AI_DECISION_MAX_TOKENS`,
`AI_DECISION_HEAD_TOKENS`). `--queue N` bounds the requests waiting per
model; `--idle-seconds N` shortens the five-minute idle unload for tests.

Two offline subcommands run the engine's tokenizer and Laya encoder on JSON
from stdin, for the parity harness:

```sh
papergo-engine tokenize TOKENIZER.json [--special] [--prompt MODEL:query|document] < texts.json
papergo-engine laya-encode LAYA_DIR < requests.json
```

## Parity and benchmarks

```sh
python3 -I engines/dotnet/bench/harness.py build ocr tokenizers embeddings decisions bench \
  --out .cache/bench --pages page0.png page1.png page2.png
```

`build` compiles the Go reference CLIs (`cmd/ocr`, `cmd/embed`, `cmd/decide`)
and `bench/goref` with the native environment and renders the OCR fixtures of
`internal/ocr/paddle`'s tests. The harness never runs two model processes at
once and waits for free memory before heavy steps. See [RESULTS.md](RESULTS.md).
