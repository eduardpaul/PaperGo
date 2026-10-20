# Idle native memory

Measured on 15 October 2026 on Linux amd64/WSL2, an Intel Core i7-10870H with four logical CPUs, Go 1.26.0 and the pinned native SDKs. RSS includes resident Go/native allocations and mapped code/data. These are disposable local processes, with no container memory limit; other host activity was not controlled. The figures are memory observations, not throughput comparisons or deployment limits.

The baseline is cgo migration commit `604a3e3`. Both versions use the same native SDK files. The server fixture creates 200 invoice-like items through the HTTP API, waits for both domain events and indexing workflows to finish, then waits 35 seconds at each idle stage. Local AI is disabled in these server measurements. The empty server still includes the administrator directory, embedded UI, workflow runner and search store.

| Workload | Baseline RSS | Optimized RSS |
| --- | ---: | ---: |
| Empty API, zvec | 223 MiB | 185 MiB |
| API with 200 items, idle after searches | 574 MiB | 262 MiB |
| API with 200 items, idle after restart | 294 MiB | 237 MiB |
| Native zvec test, 2,000 documents and 768d vectors, after close/reopen and queries | 894 MiB | 333 MiB |

The default SQLite backend uses about **75 MiB** empty and **86 MiB** idle after searching 200 items. The zvec stress test is a whole Go test process, not the API, and deliberately includes indexing, concurrent reads/writes, 20,000 readable scopes and close/reopen. Its 6 MiB of cached vectors do not describe the full footprint.

The table measures short idle periods with the native indexes still loaded. In a separate 200-item API run, RSS fell from **268 MiB after searching to 120 MiB after 335 seconds idle**, while search status was polled every five seconds. The next search reopened the persisted indexes, returned the expected results in **316 ms** including the HTTP request, and brought RSS to **266 MiB**. Local AI was disabled and no Go collection was forced. The unloaded process still retains the Go catalog, cached vectors and shared native runtime resources; these figures describe this fixture, not a fixed server budget.

## Findings and changes

- **Unused full-text field.** The application queries `metadata` and `content`; title/keyword weighting uses the Go catalog. Only the ABI test queried the additional `heading` field. The native schema now contains the two fields used by the application.
- **Allocator retention.** In the baseline diagnostic probe, Go's live heap was about 23 MiB after collection. glibc still held roughly 432 MiB on native free lists. Go collection lowered RSS only slightly; explicitly trimming the C allocator lowered it by about 331 MiB. This identifies a substantial retention cost, without establishing that every remaining allocation is necessary or that long-running workloads cannot leak.
- **Native allocation policy.** On glibc, PaperGo uses two allocator arenas and a fixed 128 KiB trim threshold, preventing automatic threshold growth from retaining large temporary indexing buffers. The existing 30-second zvec maintenance pass and store close return free allocator pages. Idle model eviction does the same. macOS asks its allocator to relieve unused pages; Windows keeps the CRT's normal allocator policy. The measured savings here apply to Linux.
- **Live SDK overhead.** zvec's full-text implementation creates multiple RocksDB column families per field. Its [pinned RocksDB configuration](https://github.com/alibaba/zvec/blob/8321c1314a559fd5f909e92498f43e5194bf9b99/src/db/common/rocksdb_context.cc) uses a hash skip list with one million buckets. Reducing the field count helps, but live native tables still explain part of the gap between SQLite and zvec. The SDK's configured memory-pool limit is not an RSS limit on all its allocations.
- **Container budget.** The SDK buffer pool is capped at 128 MiB, or its detected host/container budget when smaller. The native ABI and 2,000-vector soak are also checked under a 512 MiB Docker limit. That is a search-only test, not a 512 MiB budget for an API with loaded AI models.
- **Idle native indexes.** After five minutes without a native search or write, the 30-second maintenance pass flushes and closes both zvec collections. The next native search or write reopens the persisted indexes. The Go catalog and cached vectors remain resident; status polling, exports and catalog-only reads do not wake zvec or extend its idle deadline. The ownership lease remains held, and active operations complete before eviction.

The allocator settings use the documented [glibc allocation parameters](https://sourceware.org/glibc/manual/latest/html_node/Malloc-Tunable-Parameters.html). This policy trades some allocator concurrency/cache reuse for lower retained memory. It adds no source patches or replacement native SDK builds.

## Idle local models

OCR and local embedding sessions unload after **five minutes without inference**. Active inference prevents eviction; the next request reloads its model. Empty embedding batches do not extend the idle deadline. Explicit embedding `Close` still prevents reuse.

In a separate process using Gemma Q4 and the bundled OCR models, RSS was **335 MiB after inference**, **60 MiB after five minutes and ten seconds idle**, and **323 MiB when embedding reloaded**. No forced Go collection was used. This probe excludes the API and search store, so it is not the complete server budget. Native race tests also verify that inference produces the same result after automatic unloading/reloading.

## Reproduce

From the repository root on Linux:

```sh
python3 scripts/native.py
source .tools/native/env.sh
npm ci --prefix web
npm --prefix web run build
go build -trimpath -ldflags='-s -w' -o bin/papergo ./cmd/api
go build -trimpath -ldflags='-s -w' -o bin/papergo-admin ./cmd/admin
python3 scripts/memory.py --search-store sqlite --idle-seconds 35 --output .cache/memory-sqlite.json
python3 scripts/memory.py --search-store zvec --idle-seconds 35 --output .cache/memory-zvec.json
python3 scripts/memory.py --search-store zvec --idle-seconds 1 --unload-seconds 335 --output .cache/memory-zvec-unload.json
PAPERGO_ZVEC_SOAK=1 go test ./internal/dal/search/zvecstore -run '^TestRealDimensionsSoak$' -count=1 -v
```

Run measurements sequentially. `scripts/memory.py` initializes and removes its own temporary database, blobs and indexes. It never opens the configured application database. `--items` changes the fixture size; workflow completion can take longer with larger datasets.

To repeat the five-minute model probe:

```sh
python3 scripts/fetch-embedding-models.py embeddinggemma-2
mkdir -p .cache/memory-model-probe
cp docs/performance-artifacts/memory/models_probe.go.txt .cache/memory-model-probe/main.go
EMBEDDING_MODELS_ROOT="$PWD/.tools/embedding/models" go run ./.cache/memory-model-probe
```

[results.json](results.json) retains the raw server, soak, allocator and model measurements, including binary/native fingerprints. The model probe source is [models_probe.go.txt](models_probe.go.txt). The native lifecycle suite is documented in [zvec validation](../zvec/README.md).
