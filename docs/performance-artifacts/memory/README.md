# Idle native memory

Measured on 15 October 2026 on Linux amd64/WSL2, an Intel Core i7-10870H with four logical CPUs, Go 1.26.0 and the pinned native SDKs. RSS includes resident Go/native allocations and mapped code/data. These are disposable local processes, with no container memory limit; other host activity was not controlled. The figures are memory observations, not throughput comparisons or deployment limits.

The server fixture creates 200 invoice-like items through the HTTP API, waits for both domain events and indexing workflows to finish, then waits 35 seconds at each idle stage. Local AI is disabled in these server measurements. The empty server still includes the administrator directory, embedded UI, workflow runner and SQLite search store.

| Workload | RSS |
| --- | ---: |
| Empty API | 75 MiB |
| API with 200 items, idle after searches | 86 MiB |
| API with 200 items, idle after restart | 79 MiB |

## Native allocation policy

On glibc, PaperGo uses two allocator arenas and a fixed 128 KiB trim threshold, preventing automatic threshold growth from retaining large temporary inference buffers. Idle model eviction returns free allocator pages. macOS asks its allocator to relieve unused pages; Windows keeps the CRT's normal allocator policy.

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
python3 scripts/memory.py --search-store sqlite --idle-seconds 35 --output .cache/memory-sqlite.json
```

Run measurements sequentially. `scripts/memory.py` initializes and removes its own temporary database, blobs and indexes. It never opens the configured application database. `--items` changes the fixture size; workflow completion can take longer with larger datasets.

To repeat the five-minute model probe:

```sh
python3 scripts/fetch-embedding-models.py embeddinggemma-2
mkdir -p .cache/memory-model-probe
cp docs/performance-artifacts/memory/models_probe.go.txt .cache/memory-model-probe/main.go
EMBEDDING_MODELS_ROOT="$PWD/.tools/embedding/models" go run ./.cache/memory-model-probe
```

[results.json](results.json) retains the raw server and model measurements, including binary fingerprints. The model probe source is [models_probe.go.txt](models_probe.go.txt).
