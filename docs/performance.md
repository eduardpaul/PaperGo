# Self-hosting performance

Measured on 13 October 2026 against application commit `5947e87`. PaperGo's
small process footprint suits self-hosting; CPU and query shape are the main
constraints under sustained load. The measurements and sizing estimates below
include its embedded workflow runner. Updated cgo idle-memory measurements,
including zvec and five-minute model unloading, are in the
[native memory report](performance-artifacts/memory/README.md); the request-load
measurements below have not been rerun with that allocator policy.

## Measurement conditions

- Linux x86-64 under WSL2, four logical CPUs on an Intel Core i7-10870H,
  approximately 9.7 GiB VM memory, ext4 storage on a virtual disk. No CPU or
  memory cgroup limit was applied. Other host activity was not controlled.
- Go 1.26.0, native binaries built with `-trimpath` and
  `-ldflags='-s -w'`. The Dockerfile pins a newer Go; the image
  itself and ARM devices were not measured.
- Fresh temporary databases initialized using the actual schema and admin
  binary. User data was not opened or changed. All application defaults were
  used: 32 admitted ordinary requests, eight application database connections,
  eight runner connections, SQLite WAL with `synchronous=FULL`, 64 MiB upload
  limit and four concurrent workflow workers.
- HTTP measurements use development bearer authentication as an organization
  administrator. They include authentication, serialization, request logging,
  the real database, and background dispatch. No custom workflows execute.
  The service benchmarks also exercise an ordinary reader with fragmented ACLs
  and mixed draft/published surfaces.
- Collections contain small invoice-like items with two indexed fields and a
  keyword. HTTP collection publishing and metadata versioning are off; library
  versioning uses its default. Write stages add items, so collection size grows
  after the initial read stages. An extended run also edits items repeatedly.
- Closed-loop clients reuse HTTP connections on localhost, with no think time.
  Concurrency means simultaneous active requests, not the number of people.
  Stages run sequentially; no other benchmark runs alongside them.
- CPU is **100% per logical core**, so 320% means approximately 3.2 cores.
  Memory is process resident memory (RSS), sampled every 50 ms. It includes
  more than the Go heap, but excludes kernel file cache and other processes.
  Brief peaks may be missed. WSL2 does not expose `/proc/<pid>/io` here; disk
  throughput/IOPS are therefore unmeasured, and raw I/O fields are null.
- Latencies include the client round trip. Short stages and 20-iteration
  service benchmarks establish a baseline, not a long-term capacity guarantee.
  Transfers use synthetic bytes with a PDF signature; OCR, PDF parsing, real
  network latency, TLS, reverse proxies, OIDC providers, backup load, and browser
  CPU/memory are outside the measurements.

## Results

The empty server used **43 MiB RSS**, started ready in approximately **0.20 s**,
and used **0.3–0.6% of one core** while idle. After seeding, idle RSS was
approximately **53 MiB at 2,000 items** and **58 MiB at 20,000 items**. Four
idle live-event connections raised idle CPU to approximately 0.6–0.7% of one
core. The stripped API binary, including the frontend, was **41.2 MiB**.

Across the two runs, all **38,866 measured HTTP requests succeeded**. Setup and
readiness requests are outside this count. Ordinary concurrent browsing peaked
at approximately **81–88 MiB RSS**; the largest measured footprint was
**170 MiB** with eight simultaneous 500-row pages. After load, idle RSS settled
to approximately **66–71 MiB**. These are short-run observations, not a proven
minimum memory limit.

### HTTP queries

Each latency pair below is median / 95th percentile, in milliseconds. Queries
return up to 50 rows unless otherwise specified.

| Workload | 2,000 items, ms | 20,000 items, ms |
| --- | ---: | ---: |
| Browse, one active client | 5.8 / 7.0 | 6.6 / 8.8 |
| Indexed integer sort | 6.2 / 7.4 | 6.7 / 8.5 |
| Exact integer filter, sorted by modification time | 6.2 / 7.4 | 47.7 / 60.1 |
| Broad full-text search, sorted by name | 7.0 / 8.5 | 14.9 / 17.1 |
| Page with exact authorized total | 10.3 / 12.5 | 36.0 / 42.6 |
| Browse, eight active clients | 24.5 / 32.9 | 27.2 / 40.0 |
| Browse, 32 active clients | 90.5 / 174.8 | 94.2 / 185.9 |
| Browse 500 rows, one active client | — | 33.6 / 39.2 |
| Browse 500 rows, eight active clients | — | 125.6 / 169.3 |

At eight clients, ordinary browsing delivered **280–320 pages/s** and consumed
approximately **3.0–3.2 cores**. Increasing to 32 clients delivered approximately
**311–326 pages/s**, while substantially increasing latency. At 2,000 items,
64 clients delivered 334 pages/s with a 369 ms p95. The overload rejection path
was not triggered by these workloads. Raising concurrency buys little throughput
once CPU/database reads are saturated.

Ordinary pages cost approximately **7–11 ms of server CPU per request** in these
runs. As a rough linear estimate, ten ordinary page requests per second would
use 7–11% of one core, plus idle work. At 20,000 items, the selective sorted query
cost about **51 ms CPU/request** and a page with an exact total cost about
**39 ms CPU/request**. Request rate and query mix are better sizing inputs than
registered-user count. Use ordinary page sizes and avoid exact totals on every
refresh; `total_limit` offers an explicitly bounded count where appropriate.

### Writes and transfers

Bulk seed batches of 100 small items achieved approximately **278 items/s at
2,000 items** and **240 items/s at 20,000 items** with the runner active. These
rates include HTTP and ongoing dispatch work, but exclude waiting for the final
event backlog to drain. The existing six-indexed-field bulk-create benchmark
achieved 256 items/s, illustrating that schema and transaction workload matter.

At 20,000 items, repeated single-item metadata edits achieved **102 edits/s**
with one client (9.6 ms median, 11.7 ms p95). Eight edit clients achieved only
**126 edits/s**, with 60.7 ms median and 78.3 ms p95. Metadata versioning was off;
major versioning adds history work and was not measured in these edit stages.

With eight clients mixing 80% reads and 20% creates, the 20,000-item run delivered
259 requests/s: read p95 was 36 ms and create p95 was 88 ms. With 32 clients,
throughput rose only to 272 requests/s: read p95 was 32 ms, but create p95 rose to
**567 ms**. Write queuing becomes visible well before memory is exhausted.
The mix is approximately 80/20; workers can drain a final in-flight write after
the stage deadline.

A single 64 MiB upload took **247–484 ms** across the runs and kept RSS at
approximately **70–75 MiB**, supporting the streaming implementation. Four
simultaneous 16 MiB uploads also kept RSS below 77 MiB. These very small transfer
samples are not percentile estimates. Cached 16 MiB downloads on loopback
achieved roughly 1–3 GiB/s, but the four-client load generator itself consumed
approximately one core. Do not use those download rates as a disk, LAN or VPS
bandwidth prediction.

### Disk and frontend

| Storage snapshot while server runs | 2,000-item run | 20,000-item run |
| --- | ---: | ---: |
| Database after seeding and dispatch drain | 16.1 MiB | 150.8 MiB |
| WAL after seeding and dispatch drain | 7.6 MiB | 18.8 MiB |
| Database after all workloads | 27.1 MiB | 158.9 MiB |
| WAL after all workloads | 215.0 MiB | 100.4 MiB |
| Final item count | 3,515 | 20,723 |

The fixture uses roughly **8 KiB of metadata database per small item**, including
indexes, projections, audit records and domain events. This is an estimate for
this schema, not a general per-document allowance. Stored file bytes are extra.
WAL size is the physical file high water, not a count of currently uncheckpointed
bytes. Connections set `wal_autocheckpoint=256` and `journal_size_limit=1048576`.
A finished passive checkpoint copies frames and leaves the file in place. The
next write restarts that journal and retains about 1 MiB. A large transaction
keeps the frames it wrote, and a reader that blocks the checkpoint leaves a
larger `-wal` file until a later write. `GET /v1/storage/status` runs a passive
checkpoint and reports busy, log, and checkpointed frame counts plus the journal
size.
`PRAGMA wal_checkpoint(TRUNCATE)` during a quiet period reclaims that file and
leaves the main database file as it is. `VACUUM` rewrites the main database to
pack free pages; it is a separate operation, and PaperGo does not schedule it.
Allow journal headroom for an in-flight transaction or a blocked checkpoint,
and size the main file from retained data.

`GET /v1/storage/status` runs the passive checkpoint, then splits the main file
into pages `dbstat` can name and freelist pages it omits. Accounted bytes plus
freelist bytes equal `page_count * page_size`. When the checkpoint finishes, the
main file is that size. A blocked checkpoint can leave newer pages only in the
journal, so the file stays smaller until a later checkpoint copies them. Unused
bytes are slack inside pages that still belong to a table or index; they are not
freelist pages, and deleting whole pages moves that space to the freelist. The
same response reports the search store separately and sums available blob sizes
once per SHA-256. Those file bytes live outside SQLite. A fresh database in this
tree was 1,298,432 bytes across 317 pages, with 1,168,059 bytes of slack and no
freelist pages, beside a 238,968-byte SQLite search file that held no documents.
File count does not predict the database file.

Six uploaded file versions represented 144 MiB of logical content, with only
80 MiB of distinct content. The 2,000-item run ended with 80 MiB of physical files;
the shorter 20,000-item run still had 144 MiB while redundant upload objects
awaited cleanup. Duplicate uploads therefore still need temporary disk space.

The embedded frontend build contains approximately **13 MiB of assets**, 12 MiB
of them the workflow script editor (Monaco and its TypeScript checker), which
loads only when a script is opened. Assets referenced directly by the initial
HTML total **652 KiB uncompressed**, or **206 KiB with gzip** measured locally.
Additional routes load further chunks. The application serves its JavaScript,
CSS, font and other text assets gzip-compressed to clients that accept gzip,
compressing each file once on its first such request; the whole build
compresses to about 3 MiB. Browser rendering performance was not tested.

### Permission and service benchmarks

These benchmarks operate below HTTP, with half the collection published and
one item in every 50 under a separate ACL scope. Values are mean time per
operation; they are not HTTP latency percentiles. The user is a workspace owner
or an ordinary reader, rather than the administrator used for HTTP testing.

| Service query, up to 50 rows | 2,000 items | 20,000 items |
| --- | ---: | ---: |
| Name sort, owner/head | 5.4 ms | 7.7 ms |
| Indexed integer sort, owner/auto | 8.7 ms | 28.3 ms |
| Indexed integer sort, fragmented reader/auto | 26.1 ms | 68.7 ms |
| Selective integer filter, modification-time sort | 6.0 ms | 60.9 ms |
| Search/name sort, fragmented reader/auto | 25.1 ms | 85.0 ms |
| Exact authorized total, owner/auto | 13.6 ms | **612.8 ms** |

The exact-total result shows that the administrator HTTP fixture understates
counting costs on a publication-enabled collection with many ACL scopes.
Default paged reads avoid that full count. Ordinary service pages allocated
approximately **0.54–0.60 MiB and 11,000 objects per query**, even as collection
size increased. This allocation rate can contribute to CPU/GC pressure without
turning into permanent RAM growth.

A separate benchmark spanning **100 collections and 1,000 total items**,
returning 50 matches plus an authorized total, took **65.7 ms** and allocated
**7.8 MiB / 75,337 objects per query**. Smart folders spanning many collections
need their own capacity budget.

At 2,000 items, single-item edits in the index-build benchmark averaged
**5.2 ms** on its idle writer and **11.4 ms** while index builds ran; recorded
maxima were **7.8 ms** and **15.0 ms**. This verifies additional contention in
that bounded fixture, not a maximum latency guarantee for large rebuilds.

Raw evidence: [2,000-item HTTP run](performance/http-2000.json),
[20,000-item HTTP run](performance/http-20000.json),
[2,000-item service benchmarks](performance/bench-2000.txt),
[20,000-item service benchmarks](performance/bench-20000.txt), and
[frontend sizes](performance/frontend.json).

## Runtime behavior relevant to hosting

PaperGo serves the UI and API from one binary. Node.js is needed to build the
frontend, and there is no separate database service at runtime. The workflow
engine is embedded and maintains its state in the same SQLite file.

SQLite provides concurrent WAL readers and one writer. Request writes and
workflow step writes queue through one in-process writer slot before they take
a database connection, so waiting writers never hold the connections readers
need. Step writes yield the slot to request writes, so under sustained load
background workflow runs progress more slowly rather than delaying requests.
`synchronous=FULL` preserves durability, making storage latency relevant to
write-heavy hosting. More CPU cores can help parallel reads, but cannot create
additional SQLite writers. Use local SSD-backed storage and measure the actual
host before setting an import or workflow throughput target.

Ordinary API/WebDAV requests have a 32-request admission pool and a one-second
queue wait before a 503. Reads have a 30-second application timeout. Mutations
are not time-boxed by that read timeout. Content transfers bypass the ordinary
admission pool and use progress deadlines, so `MAX_IN_FLIGHT` does not cap
simultaneous uploads/downloads. Bound transfer concurrency at the reverse proxy
when setting a resource budget. Live events have a separate limit of 64 streams
per process and four per user; each stream polls once per second.

File uploads hash and copy to disk using bounded buffers rather than loading
the entire file into RAM. Physical bytes with equal SHA-256 and size are shared
across the organization; each item retains its own file-version metadata.
Uploads still write and hash the incoming bytes before deduplication, and
redundant objects await background cleanup. Budget disk for distinct retained
file contents, the SQLite database and WAL, backups,
temporary upload/import/export files, and logs. Current document language hints
are metadata; PaperGo does not run an OCR engine in these measurements.

Collections can retain 1–500 metadata versions per item (default 50 when
versioning is enabled). With versioning off, replaced files enter byte reclamation
after the grace period. With versioning enabled, file versions are retained
independently; `max_versions` does not cap stored file bytes. Dispatched events and finished workflow runs use the
30-day default run retention (`RUN_RETENTION`); successful automatic system runs use `SYSTEM_RUN_RETENTION` (default 24h) and reusable answers use `RESULT_CACHE_TTL` (default 720h). Cleanup is hourly, so an eligible row can wait up to one hour. Live changes use seven days. Ordinary audit
history has no general automatic expiry, and publication snapshots stay while the item is live. Retention deletions free SQLite pages
for reuse; they do not inherently shrink the database file. The server logs
every request, so configure log rotation independently of application retention.

## Sizing and next measurements

These are **starting estimates**, inferred from the measurements rather than
validated minimum configurations. Host RAM includes the OS, file cache and
basic proxy overhead; an identity provider needs its own budget.

| Deployment | Suggested starting host | Interpretation |
| --- | --- | --- |
| Personal, light interactive use | 1 vCPU, 512 MiB–1 GiB RAM | Prefer 1 GiB for OS/cache headroom. A single CPU was not independently benchmarked. |
| Small team / tested 20,000-item scale | **2 vCPU, 1 GiB RAM, local SSD** | Practical default; ordinary request rates should be comfortably below the saturation tests. |
| Sustained heavy reads, broad queries or automation | 4 vCPU, 2 GiB+ RAM | Benchmark the real workload; extra cores will not remove the single-writer constraint. |

For the measured SQLite workloads with local inference disabled, **512 MiB is
a reasonable initial PaperGo container budget**, with headroom above the
measured 170 MiB peak. Those request-load tests did not enforce a hard limit;
500-row pages at greater concurrency, exports, imports and active workflows
can have larger footprints. Leave `MAX_IN_FLIGHT=32` initially, bound client
and transfer concurrency, and avoid increasing it to chase throughput. A
low-volume installation does not need to provision for 300 requests/s.

Calculate disk as **distinct retained file content + database + WAL headroom +
temporary operations + backups + rotated logs**. Version history and duplicate
upload cleanup mean it cannot be sized from current document sizes alone.

Before treating these estimates as production limits, run the harness on the
intended hardware and storage. Use representative schemas, permissions,
document sizes, workflows and request rates. A longer soak test, hard memory
limits, a separate load-generator host and production authentication are the
next capacity checks; no leak or out-of-memory conclusion follows from these
short runs.

Performance work should first target query plans that walk a broad sort index
for a selective filter, totals that count every authorized match, and ACL-heavy
multi-collection queries. The service benchmarks show substantial allocations
per page; profiling hydration and permission checks can identify opportunities
to reduce CPU and allocation rate. Keep bulk transactions bounded and observe
writer wait and dispatch backlog during imports and index rebuilds. Adding
replicas or changing the database is not required to address the measured small
self-hosting workloads.

## Reproduce

Build the current frontend and binaries from the repository root:

```sh
npm ci --prefix web
npm --prefix web run build
mkdir -p .cache/performance
python3 scripts/native.py
source .tools/native/env.sh
python3 scripts/build-binaries.py --output-dir .cache/performance
python3 scripts/build-assets.py --output-dir .cache/performance/assets
go test -c -o .cache/performance/dms.test ./internal/dms
```

The Linux-only [HTTP harness](../scripts/performance.py) creates and removes its
own temporary data, starts a loopback server, clears inherited application
configuration, and writes raw process/resource measurements to JSON. Run these
commands **one at a time**:

```sh
python3 scripts/performance.py --binary .cache/performance/papergo --admin .cache/performance/papergo-admin --items 2000 --seconds 10 --idle-seconds 30 --concurrency 1 8 32 64 --output .cache/performance/http-2000.json
python3 scripts/performance.py --binary .cache/performance/papergo --admin .cache/performance/papergo-admin --items 20000 --seconds 5 --idle-seconds 10 --concurrency 1 8 32 --extended --output .cache/performance/http-20000.json
PAPERGO_BENCH_ITEMS=2000 .cache/performance/dms.test -test.run='^$' -test.bench='BenchmarkCollectionQuery|BenchmarkWriteDuringIndexBuild|BenchmarkItemUpdates|BenchmarkBulkCreate|BenchmarkSmartFolderQuery100Collections' -test.benchtime=20x -test.benchmem
PAPERGO_BENCH_ITEMS=20000 .cache/performance/dms.test -test.run='^$' -test.bench='^BenchmarkCollectionQuery$' -test.benchtime=20x -test.benchmem
```

The service fixtures do not run the workflow dispatcher. In particular,
`BenchmarkItemUpdates` accumulates undispatched events and revisions, so its
repeated-edit throughput is a backlog stress case. Use the HTTP extended edit
stages, which run the dispatcher, for a closer estimate of normal server edits.
Allocation `B/op` is bytes allocated per operation, not retained process RAM.
