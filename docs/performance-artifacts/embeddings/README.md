# Local embedding comparison

Measured on **15 October 2026** using PaperGo's Go engine, CPU Q4 exports, one inference thread and a 512-token input limit. The machine is a four-logical-CPU Intel Core i7-10870H WSL2 environment with Go 1.26.0, ONNX Runtime 1.23.2 and the cgo tokenizer SDK daulet/tokenizers v1.27.0. Each engine runs in its own process. Other Go tests/builds were finished before this recorded run. Model files may be in the OS page cache: “cold” means a fresh process loading its native state, not a cleared disk cache.

The fixture has 30 synthetic DMS passages and 60 hand-labelled queries: 30 English, 16 Spanish and 14 Catalan, retrieving English documents. Distractors include paid/unpaid invoices, approved/draft budgets, payment/delivery confirmations, different leave policies, and warranty/return rules. There is one relevant document per query. All passages fit the input limit. Quality metrics use the first set of queries; three repetitions provide 180 warm-query latency measurements per model. Documents are embedded sequentially once after the queries.

| Measurement | Gemma 2 Q4, 768d | Harrier 0.6B Q4, 1024d |
| --- | ---: | ---: |
| Configure engine | 0.004 ms | 0.004 ms |
| First query, including loading | 1.56 s | 1.71 s |
| Warm query p50 | 100 ms | 417 ms |
| Warm query p95 | 116 ms | 500 ms |
| Mean document embedding | 170 ms | 520 ms |
| Document throughput | 5.89/s | 1.92/s |
| Peak process RSS | 364 MiB | 447 MiB |
| Installed tokenizer + graph + weights | 197 MiB | 389 MiB |
| Recall@1 | 56/60 (93.3%) | 55/60 (91.7%) |
| Recall@5 | 60/60 (100%) | 60/60 (100%) |
| Mean reciprocal rank | 0.964 | 0.953 |

Gemma's **256-dimensional** vectors also achieve 56/60 Recall@1, 100% Recall@5 and 0.964 MRR on this fixture after truncation and re-normalization. They require 1 KiB per stored float32 vector, compared with 3 KiB at 768d; the inference model is the same. The native integration test separately verifies the engine's 256d output against truncated reference vectors.

For this CPU deployment, **Gemma 2 Q4 at 768d is the default recommendation**: it shares OCR's ONNX Runtime, uses smaller installed artifacts, and delivers about 4.2× faster median queries and 3.1× higher document throughput here. Loading is deferred until the first real request, which includes tokenizer setup and model initialization; it is not free. The statically linked Hugging Face tokenizer preserves the model's exact tokenization without a Python/Node process.

This small synthetic set is a functional check, not evidence that Gemma has better general text retrieval quality. The models' published broader evaluations and a representative real document corpus remain relevant to a quality-led choice. The [Microsoft card](https://huggingface.co/microsoft/harrier-oss-v1-0.6b) and [Google card](https://huggingface.co/google/embeddinggemma-2) report their own evaluation results; those full-checkpoint results are separate from this Q4 smoke test. Missed queries, ranks, per-language scores, model revisions, binary/fixture fingerprints and unrounded measurements are retained in [results.json](results.json).

## Reproduce

From the repository root:

```bash
python3 scripts/native.py
source .tools/native/env.sh
python3 scripts/fetch-embedding-models.py all

go build -o .tools/embedding/embed ./cmd/embed
python3 scripts/embedding-benchmark/compare.py
```

The installer downloads only the pinned text exports, checks SHA-256 and verifies cached artifacts. Both models use their documented query instructions; Gemma documents use the title/text template, and Harrier documents use a title followed by the plain passage. Normalized vectors are scored by dot product. The comparison harness uses the actual Go implementation, including tokenizer time, and measures peak RSS independently for each process. It needs Python 3.11+ on Linux/macOS with no third-party Python packages.

Native reference and race tests are documented in [Local embeddings](../../embeddings.md). They compare token IDs and every vector component with independently generated Python HF Tokenizers/ONNX Runtime results (tolerance 1e-5); this checks implementation agreement for the same Q4 export, rather than fidelity to the original unquantized weights. Native inference was tested on Linux amd64.
