# Local text embeddings

`internal/embedding` provides lazy local text embeddings using **EmbeddingGemma 2**, with **Harrier-OSS-v1-0.6B** available for comparison. With `AI_EMBEDDINGS_PROVIDER=local`, it is the embedding model of [semantic and hybrid search](search.md#semantic-and-hybrid-search) (`internal/ai`): `search.embed` and the embedding sweep embed file chunks as documents, and searches are embedded as queries. `cmd/embed` and the comparison harness use the engine directly.

## Runtime choice

Use the **text-only ONNX Q4 export on CPU**, sharing `internal/onnx` and the pinned ONNX Runtime 1.23.2 library with PaddleOCR. It keeps inference in the Go process, builds with cgo, and needs no Python, Node, GPU or model server at runtime. The ONNX binding supports float32 and int64 inputs, multiple inputs/outputs, external weight files, and context cancellation.

EmbeddingGemma 2 has a 270M-parameter text component, separate from its vision and audio encoders. Its export includes the learned 512→768 projection and pooled `sentence_embedding` output. Only its text graph and weights are installed. The graph requires empty `[0, 512]` image, video and audio feature tensors for text requests; their encoders are never loaded. Q4 weights use float32 activations. Avoid FP16 for Gemma: its activation range can produce invalid or degraded embeddings. [Google model card](https://huggingface.co/google/embeddinggemma-2), [ONNX export and precision guidance](https://huggingface.co/onnx-community/embeddinggemma-2-ONNX).

Harrier uses a decoder-only architecture with last-token pooling, a 32,768-token context and 1,024-dimensional embeddings. Its Q4 export includes pooling; neither model's sentence output needs pooling again. [Microsoft model card](https://huggingface.co/microsoft/harrier-oss-v1-0.6b), [Harrier ONNX export](https://huggingface.co/onnx-community/harrier-oss-v1-0.6b-ONNX).

The tokenizer uses Hugging Face's Rust implementation through [`daulet/tokenizers` v1.27.0](https://github.com/daulet/tokenizers/tree/v1.27.0), statically linked through cgo. It reads the installed `tokenizer.json`, including special tokens, Unicode and byte fallback. Inference never downloads libraries or models.

Other deployment choices add costs for this app:

| Approach | Assessment |
| --- | --- |
| Shared ONNX Runtime + native tokenizer | Chosen: reuses OCR infrastructure, exact tokenizer behavior, CPU Q4, no separate process |
| SentenceTransformers / PyTorch | Useful as a reference; adds Python and a substantial runtime to deployment |
| Transformers.js | Convenient ONNX integration; adds Node or browser inference and a second runtime boundary to the Go server |
| llama.cpp / GGUF | Useful for deployments requiring its accelerator support; adds a native inference runtime alongside OCR |
| Hand-written or partial Go tokenizer | Smaller installation, but requires proving every tokenizer operation and Unicode case against the reference |

## Install and try

The standalone executable and Docker image include both text-only Q4 presets and their tokenizers. Set `AI_EMBEDDINGS_PROVIDER=local` and choose `AI_EMBEDDINGS_MODEL=embeddinggemma-2` (default) or `harrier-oss-v1-0.6b`; the launcher selects the extracted model automatically. `AI_EMBEDDINGS_MODELS_PATH` selects an explicit alternative. Inference never downloads models. For direct Go development commands, install the assets below; Python 3.11+ is needed only for build-time installation and the comparison harness. The installer verifies pinned revisions and SHA-256 checksums, including cached files.

```bash
python3 scripts/native.py
source .tools/native/env.sh
python3 scripts/fetch-embedding-models.py embeddinggemma-2

go run ./cmd/embed -models .tools/embedding/models/embeddinggemma-2 <<'JSON'
{"queries":["Which electricity bill has been paid?"],"documents":[{"title":"Electricity invoice","text":"The September electricity bill was paid in full on 3 October."}]}
JSON
```

The native installer covers Linux amd64/arm64, macOS ARM64 and Windows amd64. It downloads pinned tokenizer static libraries for Linux/macOS and builds the pinned Rust crate for Windows. There is no tokenizer runtime library path.

Use `-model harrier-oss-v1-0.6b -models .tools/embedding/models/harrier-oss-v1-0.6b` after installing that model. The CLI returns vectors and timing information, including the first query's library/tokenizer/model loading cost.

## Engine behavior

`embedding.New(Config)` validates settings only: it does not open files, hash weights, download or load libraries. The first nonempty request loads the tokenizer and model. Empty batches and already-cancelled requests load nothing. Loading failures release partial state and can be retried after fixing the installation. After five minutes without inference, the engine releases its tokenizer and model; the next request reloads them and pays the loading cost. Running inference holds off eviction. `Close` releases resources immediately and prevents reuse. The shared ONNX library/environment stays loaded for the process, as OCR requires.

An engine serializes loading and inference. Waiting calls and running ONNX inference can be cancelled. Model creation itself is a synchronous native call; cancellation is checked before subsequent inference. Passages in a batch run one at a time to bound activation memory. CPU threads default to one, configurable from 1 to 32.

Queries and documents use different retrieval prompts:

| Model | Query | Document |
| --- | --- | --- |
| Gemma 2 | `task: search result \| query: {query}` | `title: {title} \| text: {text}`; missing title is `none` |
| Harrier | `Instruct: Given a web search query, retrieve relevant passages that answer the query` + newline + `Query: {query}` | Plain passage, with a supplied title on the preceding line |

Both outputs are validated for finite values and normalized for cosine similarity. Gemma supports 768 (default), 512, 256 or 128 dimensions by retaining leading dimensions and normalizing again. Harrier uses 1,024 dimensions. Vectors from different engines or dimensions must never be mixed. `Engine.ID()` includes the model, pinned revision, quantization, dimensions and token limit for indexing keys.

Inputs default to a **512-token limit**, including task prefixes and special tokens. Longer inputs return an error and must be chunked; there is no silent truncation. The configured maximum may be raised to 8,192 for Gemma or 32,768 for Harrier, with increasing CPU and activation-memory costs. Text must be nonempty UTF-8, at most 128 KiB, without NUL. Titles are limited to 4 KiB.

## Validation and comparison

```bash
python3 scripts/fetch-embedding-models.py all
go build -o .tools/embedding/embed ./cmd/embed
python3 scripts/embedding-benchmark/compare.py

export EMBEDDING_MODELS_ROOT="$PWD/.tools/embedding/models"
PAPERGO_REQUIRE_EMBEDDING=1 PAPERGO_REQUIRE_OCR=1 \
  go test -race ./internal/embedding ./internal/ocr/paddle ./internal/onnx ./cmd/embed
```

Native embedding tests require `ONNXRUNTIME_LIB` and `EMBEDDING_MODELS_ROOT`; they skip without them unless `PAPERGO_REQUIRE_EMBEDDING=1`. Tests check lazy initialization, failed-load retry, token and vector agreement with independent Python HF Tokenizers/ONNX Runtime, model-specific prompts, dimension truncation/normalization, token limits, concurrent requests, cancellation/recovery, resource closure, and OCR plus embeddings sharing a runtime in one process.

To regenerate the independent reference vectors, install `onnxruntime==1.23.2`, `tokenizers==0.23.3` and NumPy in a development virtual environment, then run `scripts/embedding-benchmark/reference.py`. Python is not needed by the Go tests.

The comparison measures the actual Go CLI in an isolated process per model, including tokenizer time. It records configuration latency, cold first query, warm query p50/p95, document throughput, peak process RSS, artifact size, Recall@1/5 and MRR. It uses a fixed, hand-labelled synthetic DMS smoke set of 30 documents and 60 English/Spanish/Catalan queries, three query repetitions, recommended prompts, full native dimensions and CPU Q4 for both models. Gemma's 256-dimensional retrieval scores are computed by truncating and re-normalizing the same native vectors. This small set checks functionality and regressions; it cannot establish a general quality winner. [Measured results](performance-artifacts/embeddings/README.md).
