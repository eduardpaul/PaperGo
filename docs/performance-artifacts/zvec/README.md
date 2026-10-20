# Native zvec validation

Local Linux amd64 run, 2026-10-15, on a four-logical-CPU Intel Core i7-10870H WSL2 machine with Go 1.26.0 and GCC 15.2.0. The cgo binding uses the official zvec-go v0.7.0 native SDK. This is a contract/lifecycle soak on a development machine, not a production sizing benchmark.

The dataset contains 2,000 documents and distinct published chunks, 2,000 deterministic random 768-dimensional vectors and 20,000 readable scopes. A filtered semantic query selects the exact int64 value `9007199254741724` before native HNSW top-k, then verifies the expected resource and page. An unfiltered nearest-neighbor query also checks retrieval without a single-key restriction. The test explicitly seals and builds the native HNSW index before 100 timed queries, 40 concurrent queries alongside metadata writes, closes/reopens the store and checks both ANN results and complete vector reuse.

| Measurement | Local result |
| --- | ---: |
| Initial load, indexing and vector space creation | 5,058 ms |
| Filtered semantic p50 / p95 | 25.685 / 34.899 ms |
| Reopen and source catalog load | 608 ms |
| Native directory size | 41,599,004 bytes |
| Cached float32 vector bytes | 6,144,000 bytes |
| Process RSS after queries and reopen | 341,236 KiB |

Raw measurements and the native library fingerprint are in [results.json](results.json). The process loads native FTS/HNSW, its allocators and the source catalog; vector bytes alone do not describe its memory usage. Each query is checked for the expected result. These figures do not measure the relevance of an embedding model or establish performance at 200,000 items.

Reproduce using the pinned SDK:

```sh
python3 scripts/native.py
source .tools/native/env.sh
go test -race ./internal/dal/search/zvecstore ./internal/backup ./cmd/admin
PAPERGO_ZVEC_SOAK=1 go test ./internal/dal/search/zvecstore -run '^TestRealDimensionsSoak$' -count=1 -v
PAPERGO_TEST_SEARCH_STORE=zvec go test -race ./internal/dms ./internal/httpapi ./internal/scriptrun \
  -run '(Search|Content|Receipt|Contract|AIComplete|Prefix|SmartFolder|View|Listing|Query)'
go test ./internal/dal/search/zvecstore -run '^$' -fuzz '^FuzzNativeFilter$' -fuzztime=10s -parallel=2
go test ./internal/dal/search/zvecstore -run '^$' -fuzz '^FuzzNativeText$' -fuzztime=10s -parallel=2
```

The regular native suite includes shared store conformance, literal prefixes and typed boundaries, staged content, enrichments, surface/scope updates, restart, acknowledged process-exit recovery, partial-write quarantine, exclusive ownership, offline SQLite → zvec → SQLite copy across batch boundaries, cached-model activation, vector collection and native backup/restore. CI runs the full native suite on Linux amd64 and arm64, plus SDK and packaged-release smoke checks on macOS ARM64 and Windows amd64. Release archives include native SDKs for Linux amd64/arm64, macOS ARM64 and Windows amd64.
